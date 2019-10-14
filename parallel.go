package pail

import (
	"context"
	"sync"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
)

type parallelBucketImpl struct {
	Bucket
	size         int
	deleteOnSync bool
	dryRun       bool
}

type ParallelBucketOptions struct {
	Workers      int
	DryRun       bool
	DeleteOnSync bool
}

func NewParallelSyncBucket(opts ParallelBucketOptions, b Bucket) Bucket {
	return &parallelBucketImpl{
		size:         opts.Workers,
		deleteOnSync: opts.DeleteOnSync,
		dryRun:       opts.DryRun,
		Bucket:       b,
	}
}

func (b *parallelBucketImpl) Push(ctx context.Context, local, remote string) error {
	files, err := walkLocalTree(ctx, local)
	if err != nil {
		return errors.WithStack(err)
	}

	in := make(chan string, len(files))
	for i := range files {
		in <- files[i]
	}
	close(in)
	wg := &sync.WaitGroup{}
	catcher := grip.NewBasicCatcher()
	for i := 0; i < b.size; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fn := range in {
				catcher.Add(b.Bucket.Upload(ctx, remote, fn))
			}
		}()
	}

	return catcher.Resolve()

}
func (b *parallelBucketImpl) Pull(ctx context.Context, local, remote string) error {
	iter, err := b.List(ctx, remote)
	if err != nil {
		return errors.WithStack(err)
	}

	catcher := grip.NewBasicCatcher()
	items := make(chan BucketItem)
	toDelete := make(chan string)

	deleteSignal := make(chan struct{})
	go func() {
		defer close(items)

		for iter.Next(ctx) {
			if iter.Err() != nil {
				catcher.Add(errors.Wrap(err, "problem iterating bucket"))
				break
			}
			select {
			case <-ctx.Done():
				catcher.Add(ctx.Err())
				break
			case items <- iter.Item():
				continue
			}
		}
	}()

	wg := &sync.WaitGroup{}

	for i := 0; i < b.size; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range items {
				if err := b.Download(ctx, local, remote); err != nil {
					catcher.Add(err)
				}
				select {
				case <-ctx.Done():
					catcher.Add(ctx.Err())
					return
				case toDelete <- item.Name():
					continue
				}
			}
		}()
	}

	go func() {
		defer close(deleteSignal)
		keys := []string{}
		for key := range toDelete {
			keys = append(keys, key)
		}
		wg.Wait()
		if b.deleteOnSync {
			if b.dryRun {
				grip.Debug(message.Fields{
					"dry_run": true,
					"keys":    keys,
					"message": "would delete after push",
				})
			} else {
				catcher.Add(errors.Wrapf(b.RemoveMany(ctx, keys...), "problem removing '%s' after pull", remote))
			}
		}
	}()
	select {
	case <-ctx.Done():
	case <-deleteSignal:
	}
	return catcher.Resolve()
}
func (b *parallelBucketImpl) RemoveMany(ctx context.Context, keys ...string) error { return nil }
