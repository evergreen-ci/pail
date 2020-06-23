package benchmarks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/evergreen-ci/pail"
	"github.com/evergreen-ci/pail/testutil"
	"github.com/evergreen-ci/poplar"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
)

// RunBucket runs the bucket benchmark suite.
func RunBucket(ctx context.Context) error {
	prefix := filepath.Join("build", fmt.Sprintf("bucket-benchmark-%d", time.Now().Unix()))
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
type uploadPayloadConstructor func(name string, bytes int) uploadPayload
type uploadPayload func(context.Context, pail.S3Options) error

func basicPullIteration(makeBucket syncBucketConstructor, doUploadPayload uploadPayload, timeout time.Duration) poplar.Benchmark {
	return func(ctx context.Context, r poplar.Recorder, count int) error {
		for i := 0; i < count; i++ {
			opts := s3Opts()
			if err := doUploadPayload(ctx, opts); err != nil {
				return errors.Wrap(err, "uploading benchmark test case data")
			}
			if err := runBasicPullIteration(ctx, r, makeBucket, opts, timeout); err != nil {
				return errors.Wrapf(err, "iteration %d", i)
			}
		}
		return nil
	}
}

func runBasicPullIteration(ctx context.Context, r poplar.Recorder, makeBucket syncBucketConstructor, opts pail.S3Options, timeout time.Duration) error {
	b, err := makeBucket(opts)
	if err != nil {
		return errors.Wrap(err, "making bucket")
	}
	defer func() {
		grip.Error(errors.Wrap(testutil.CleanupS3Bucket(opts.Name, opts.Prefix, opts.Region), "cleaning up remote store"))
	}()

	errChan := make(chan error)
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startAt := time.Now()
	r.BeginIteration()
	defer func() {
		r.EndIteration(time.Since(startAt))
	}()

	local := "kim: TODO: some temp dir"
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

// kim: TODO:
// - feed data to bucket
// - see how fast it uploads/downloads N bytes, with max of T seconds.
func syncBucketBenchmarkSuite() poplar.BenchmarkSuite {
	var suite poplar.BenchmarkSuite
	for bucketName, makeBucket := range map[string]syncBucketConstructor{
		// kim: TODO: small bucket bucket
		// kim: TODO: large bucket bucket
		// kim: TODO: archive bucket
	} {
		for caseName, benchCase := range map[string]struct {
			numFiles                 string
			bytesPerFile             int
			uploadPayloadConstructor uploadPayloadConstructor
			timeout                  time.Duration
		}{
			// kim: TODO: write payload constructor that uploads to S3 first.
			// kim: TODO: few items, large size
			// kim: TODO: many items, small size
		} {
			suite = append(suite,
				&poplar.BenchmarkCase{
					CaseName:      fmt.Sprintf("%s-%s-%dFilesEachWith%dBytes", bucketName, caseName, benchCase.numFiles, benchCase.bytesPerFile),
					Bench:         basicPullIteration(makeBucket, benchCase.uploadPayloadConstructor(benchCase.numFiles, benchCase.bytesPerFile), benchCase.timeout),
					Count:         1,
					MinRuntime:    15 * time.Second,
					MaxRuntime:    5 * time.Minute,
					Timeout:       10 * time.Minute,
					MinIterations: 10,
					MaxIterations: 20,
					Recorder:      poplar.RecorderPerf,
				},
				&poplar.BenchmarkCase{
					CaseName:      fmt.Sprintf("%s-%s-%dFilesEachWith%dBytes", bucketName, caseName, benchCase.numFiles, benchCase.bytesPerFile),
					Bench:         basicPullIteration(makeBucket, benchCase.uploadPayloadConstructor(benchCase.numFiles, benchCase.bytesPerFile), benchCase.timeout),
					Count:         1,
					MinRuntime:    time.Minute,
					MaxRuntime:    15 * time.Minute,
					Timeout:       30 * time.Minute,
					MinIterations: 10,
					MaxIterations: 20,
					Recorder:      poplar.RecorderPerf,
				},
				// kim: TODO: push iteration
			)
		}
	}
	return suite
}

func s3Opts() pail.S3Options {
	return pail.S3Options{
		Region:     "us-east-1",
		Name:       "sync-bucket-benchmarks",
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
