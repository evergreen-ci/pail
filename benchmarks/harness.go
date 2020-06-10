package benchmarks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/evergreen-ci/pail"
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
	s := bucketBenchmarkSuite()
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

type closeFunc func() error

type bucketConstructor func(context.Context) (pail.Bucket, closeFunc, error)
type payloadConstructor func(id string) []byte

func basicThroughputBenchmark(makeBucket bucketConstructor, makePayload payloadConstructor, timeout time.Duration) poplar.Benchmark {
	return func(ctx context.Context, r poplar.Recorder, count int) error {
		for i := 0; i < count; i++ {
			if err := runBasicThroughputIteration(ctx, r, makeBucket, makePayload, timeout); err != nil {
				return errors.Wrapf(err, "iteration %d", i)
			}
		}
		return nil
	}
}

func runBasicThroughputIteration(ctx context.Context, r poplar.Recorder, makeBucket bucketConstructor, makePayload payloadConstructor, timeout time.Duration) error {
	b, cleanupBucket, err := makeBucket(ctx)
	if err != nil {
		return errors.Wrap(err, "making bucket")
	}
	defer func() {
		grip.Error(errors.Wrap(cleanupBucket(), "cleaning up bucket"))
	}()

	errChan := make(chan error)
	qctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// kim: TODO: set up bucket to be ready to perform operation (e.g. push,
	// pull, download, upload)
	// go func() {
	//     for {
	//         if qctx.Err() != nil {
	//             return
	//         }
	//
	//         // kim: TODO: replace with creating payload
	//         // j := makePayload(uuid.New().String())
	//         // if err := b.Put(qctx, j); err != nil {
	//         //     select {
	//         //     case <-qctx.Done():
	//         //         return
	//         //     case errChan <- errors.Wrap(err, ""):
	//         //         return
	//         //     }
	//         // }
	//     }
	// }()

	startAt := time.Now()
	r.BeginIteration()
	defer func() {
		r.EndIteration(time.Since(startAt))
	}()

	// kim: TODO: replace with some bucket operation
	// if err = b.Start(qctx); err != nil {
	//     return errors.Wrap(err, "starting queue")
	// }

	timer := time.NewTimer(timeout)
	select {
	case err := <-errChan:
		return errors.WithStack(err)
	case <-qctx.Done():
		return qctx.Err()
	case <-timer.C:
		// stats := b.Stats(ctx)
		// r.IncOperations(int64(stats.Completed))
	}
	return nil
}

// kim: TODO:
// - feed data to bucket
// - see how fast it uploads/downloads N bytes OR see how many bytes it can
// upload/download in T time.
func bucketBenchmarkSuite() poplar.BenchmarkSuite {
	var suite poplar.BenchmarkSuite
	for bucketName, makeBucket := range map[string]bucketConstructor{
		// "MongoDB": makeMongoDBQueue,
	} {
		for payloadName, makePayload := range map[string]payloadConstructor{
			// "Noop":                     newNoopJob,
			// "ScopedNoop":               newScopedNoopJob,
			// "MixedScopeAndNoScopeNoop": newSometimesScopedJob(50),
		} {
			suite = append(suite,
				&poplar.BenchmarkCase{
					CaseName:      fmt.Sprintf("%s-%s-15Second", bucketName, payloadName),
					Bench:         basicThroughputBenchmark(makeBucket, makePayload, 15*time.Second),
					Count:         1,
					MinRuntime:    15 * time.Second,
					MaxRuntime:    5 * time.Minute,
					Timeout:       10 * time.Minute,
					MinIterations: 10,
					MaxIterations: 20,
					Recorder:      poplar.RecorderPerf,
				},
				&poplar.BenchmarkCase{
					CaseName:      fmt.Sprintf("%s-%s-1Minute", bucketName, payloadName),
					Bench:         basicThroughputBenchmark(makeBucket, makePayload, time.Minute),
					Count:         1,
					MinRuntime:    time.Minute,
					MaxRuntime:    15 * time.Minute,
					Timeout:       30 * time.Minute,
					MinIterations: 10,
					MaxIterations: 20,
					Recorder:      poplar.RecorderPerf,
				},
			)
		}
	}
	return suite
}

// type noopJob struct {
//     job.Base
// }
//
// func newNoopJobInstance() *noopJob {
//     j := &noopJob{
//         Base: job.Base{
//             JobType: amboy.JobType{
//                 Name:    "benchmark",
//                 Version: 1,
//             },
//         },
//     }
//     j.SetDependency(dependency.NewAlways())
//     return j
// }
//
// func newNoopJob(id string) amboy.Job {
//     j := newNoopJobInstance()
//     j.SetID(id)
//     return j
// }
//
// func newScopedNoopJob(id string) amboy.Job {
//     j := newNoopJobInstance()
//     j.SetScopes([]string{"common_scope"})
//     j.SetID(id)
//     return j
// }
//
// func newSometimesScopedJob(percentScoped int) func(id string) amboy.Job {
//     if percentScoped < rand.Intn(100) {
//         return newScopedNoopJob
//     }
//     return newNoopJob
// }
//
// func init() {
//     registry.AddJobType("benchmark", func() amboy.Job {
//         return newNoopJobInstance()
//     })
// }
//
// func (j *noopJob) Run(ctx context.Context) {
//     j.MarkComplete()
// }
