package benchmarks

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/evergreen-ci/pail"
	"github.com/evergreen-ci/pail/testutil"
	"github.com/evergreen-ci/poplar"
	"github.com/evergreen-ci/utility"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
)

func buildDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filepath.Dir(file)), "build")
}

// RunSyncBucket runs the bucket benchmark suite.
func RunSyncBucket(ctx context.Context) error {
	dir := buildDir()
	prefix := filepath.Join(dir, fmt.Sprintf("sync-bucket-benchmarks-%d", time.Now().Unix()))
	if err := os.MkdirAll(prefix, os.ModePerm); err != nil {
		return errors.Wrap(err, "creating benchmark directory")
	}

	resultFile, err := os.Create(filepath.Join(prefix, "results.txt"))
	if err != nil {
		return errors.Wrap(err, "creating result file")
	}

	var resultText string
	s := syncBucketBenchmarkSuite()
	res, err := s.Run(ctx, prefix)
	if err != nil {
		resultText = err.Error()
	} else {
		resultText = res.Report()
	}

	catcher := grip.NewBasicCatcher()
	_, err = resultFile.WriteString(resultText)
	catcher.Add(errors.Wrap(err, "writing benchmark results to file"))
	catcher.Add(resultFile.Close())

	return catcher.Resolve()
}

type syncBucketConstructor func(pail.S3Options) (pail.SyncBucket, error)
type uploadPayloadConstructor func(numFiles int, bytesPerFile int) uploadPayload
type uploadPayload func(context.Context, pail.SyncBucket, pail.S3Options) error

func basicPullIteration(makeBucket syncBucketConstructor, doUploadPayload uploadPayload, opts pail.S3Options) poplar.Benchmark {
	return func(ctx context.Context, r poplar.Recorder, count int) error {
		for i := 0; i < count; i++ {
			if err := func() error {
				b, err := makeBucket(opts)
				if err != nil {
					return errors.Wrap(err, "making bucket")
				}
				defer func() {
					grip.Error(errors.Wrap(testutil.CleanupS3Bucket(opts.Name, opts.Prefix, opts.Region), "cleaning up remote store"))
				}()
				if err := doUploadPayload(ctx, b, opts); err != nil {
					return errors.Wrap(err, "uploading benchmark test case data")
				}
				if err := runBasicPullIteration(ctx, r, b, opts); err != nil {
					return errors.Wrapf(err, "iteration %d", i)
				}
				return nil
			}(); err != nil {
				return errors.WithStack(err)
			}
		}
		return nil
	}
}

func runBasicPullIteration(ctx context.Context, r poplar.Recorder, b pail.SyncBucket, opts pail.S3Options) error {

	errChan := make(chan error)
	bctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startAt := time.Now()
	r.BeginIteration()
	defer func() {
		r.EndIteration(time.Since(startAt))
	}()

	local, err := ioutil.TempDir(buildDir(), "sync-bucket-benchmarks-pull")
	if err != nil {
		return errors.Wrap(err, "making temp directory")
	}
	defer func() {
		grip.Error(errors.Wrap(os.RemoveAll(local), "cleaning up local pull directory"))
	}()
	syncOpts := pail.SyncOptions{
		Local:  local,
		Remote: opts.Prefix,
	}
	go func() {
		select {
		case errChan <- b.Pull(bctx, syncOpts):
		case <-bctx.Done():
		}
	}()

	catcher := grip.NewBasicCatcher()
	select {
	case err := <-errChan:
		catcher.Wrap(err, "pulling directory from remote store")
	case <-bctx.Done():
		catcher.Wrap(bctx.Err(), "cancelled pulling directory")
	}

	totalBytes, err := getDirTotalSize(local)
	if err != nil {
		catcher.Add(err)
	} else {
		r.IncSize(totalBytes)
	}

	return nil
}

func getDirTotalSize(dir string) (int64, error) {
	var size int64
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	}); err != nil {
		return -1, errors.Wrap(err, "summing total directory size")
	}
	return size, nil
}

func syncBucketBenchmarkSuite() poplar.BenchmarkSuite {
	var suite poplar.BenchmarkSuite
	for bucketName, bucketCase := range map[string]struct {
		constructor              syncBucketConstructor
		uploadPayloadConstructor uploadPayloadConstructor
	}{
		"Small": {
			constructor:              smallBucketConstructor,
			uploadPayloadConstructor: uploadLocalTree,
		},
		"Large": {
			constructor:              largeBucketConstructor,
			uploadPayloadConstructor: uploadLocalTree,
		},
		"Archive": {
			constructor:              archiveBucketConstructor,
			uploadPayloadConstructor: uploadLocalTree,
		},
	} {
		for caseName, benchCase := range map[string]struct {
			numFiles     int
			bytesPerFile int
			timeout      time.Duration
		}{
			"FewFilesLargeSize": {
				numFiles:     1,
				bytesPerFile: 1024 * 1024,
				timeout:      time.Hour,
			},
			// "ManyFilesSmallSize": {
			//     numFiles:     1000,
			//     bytesPerFile: 10,
			//     timeout:      time.Hour,
			// },
		} {
			suite = append(suite,
				&poplar.BenchmarkCase{
					CaseName:      fmt.Sprintf("%sBucket-Pull-%s-%dFilesEachWith%dBytes", bucketName, caseName, benchCase.numFiles, benchCase.bytesPerFile),
					Bench:         basicPullIteration(bucketCase.constructor, bucketCase.uploadPayloadConstructor(benchCase.numFiles, benchCase.bytesPerFile), s3Opts()),
					Count:         1,
					MinRuntime:    1 * time.Nanosecond, // time.Nanosecond, // We have to set this even though the test does not use it.
					MaxRuntime:    2 * time.Nanosecond, // benchCase.timeout,
					Timeout:       benchCase.timeout,
					MinIterations: 10,
					MaxIterations: 20,
					Recorder:      poplar.RecorderPerf,
				},
			)
		}
	}
	return suite
}

func uploadLocalTree(numFiles int, bytesPerFile int) uploadPayload {
	return func(ctx context.Context, b pail.SyncBucket, opts pail.S3Options) error {
		local, err := ioutil.TempDir(buildDir(), "sync-bucket-benchmarks-setup-upload")
		if err != nil {
			return errors.Wrap(err, "setting up local setup test data")
		}
		for i := 0; i < numFiles; i++ {
			file := testutil.NewUUID()
			content := utility.MakeRandomString(bytesPerFile)
			if err := ioutil.WriteFile(filepath.Join(local, file), []byte(content), 0777); err != nil {
				return errors.Wrap(err, "writing local setup test data file")
			}
		}

		if err := b.Push(ctx, pail.SyncOptions{
			Local:  local,
			Remote: opts.Prefix,
		}); err != nil {
			return errors.Wrap(err, "uploading setup test data")
		}
		return nil
	}
}

func s3Opts() pail.S3Options {
	return pail.S3Options{
		Region: "us-east-1",
		// kim: TODO: not sure if this is a legit bucket but maybe.
		Name:       "build-test-curator",
		Prefix:     testutil.NewUUID(),
		MaxRetries: 20,
	}
}

func smallBucketConstructor(opts pail.S3Options) (pail.SyncBucket, error) {
	b, err := pail.NewS3Bucket(opts)
	if err != nil {
		return nil, errors.Wrap(err, "making small bucket")
	}
	return b, nil
}

func largeBucketConstructor(opts pail.S3Options) (pail.SyncBucket, error) {
	b, err := pail.NewS3MultiPartBucket(opts)
	if err != nil {
		return nil, errors.Wrap(err, "making large bucket")
	}
	return b, nil
}

func archiveBucketConstructor(opts pail.S3Options) (pail.SyncBucket, error) {
	b, err := pail.NewS3ArchiveBucket(opts)
	if err != nil {
		return nil, errors.Wrap(err, "making archive bucket")
	}
	return b, nil
}
