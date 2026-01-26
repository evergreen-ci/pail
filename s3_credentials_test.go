package pail

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/evergreen-ci/utility"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const minimumCachedCredentialLifetime = 5 * time.Minute

func resetCredsCacheForTest() {

	credsCacheMutex.Lock()
	defer credsCacheMutex.Unlock()
	credsCache = map[string]*aws.Credentials{}
}

type fakeProvider struct {
	toReturn aws.Credentials
	err      error
}

func (f *fakeProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return f.toReturn, f.err
}

func TestSeededCredentialProvider(t *testing.T) {
	cacheKey := "test-key"
	accessKeyIDSeed := "test-seed"
	accessKeyIDNotSeed := "test-not-seed"

	creds := func(isSeed bool) aws.Credentials {
		name := accessKeyIDNotSeed
		if isSeed {
			name = accessKeyIDSeed
		}
		return aws.Credentials{
			AccessKeyID: name,
			CanExpire:   true,
			Expires:     time.Now().Add(minimumCachedCredentialLifetime + time.Minute),
		}
	}

	seedCache := func(creds aws.Credentials) {
		credsCacheMutex.Lock()
		defer credsCacheMutex.Unlock()

		credsCache[cacheKey] = &creds
	}

	t.Run("RetrieveWithNoSeed", func(t *testing.T) {
		resetCredsCacheForTest()

		p := WithSeed(&fakeProvider{toReturn: creds(false)}, cacheKey, minimumCachedCredentialLifetime)

		c, err := p.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test-not-seed", c.AccessKeyID)
	})

	t.Run("ReturnsSeed", func(t *testing.T) {
		resetCredsCacheForTest()

		seedCache(creds(true))
		p := WithSeed(&fakeProvider{toReturn: creds(false)}, cacheKey, minimumCachedCredentialLifetime)

		c, err := p.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test-seed", c.AccessKeyID)
	})

	t.Run("RetrieveWhenSeedIsCloseToExpiring", func(t *testing.T) {
		resetCredsCacheForTest()

		expiredCreds := creds(true)
		expiredCreds.Expires = time.Now().Add(minimumCachedCredentialLifetime - time.Minute)
		seedCache(expiredCreds)
		p := WithSeed(&fakeProvider{toReturn: creds(false)}, cacheKey, minimumCachedCredentialLifetime)

		c, err := p.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test-not-seed", c.AccessKeyID)

		// Test manually if we re-cached the new retrieved credentials.
		credsCacheMutex.Lock()
		cachedCred, ok := credsCache[cacheKey]
		credsCacheMutex.Unlock()

		require.True(t, ok)
		assert.Equal(t, "test-not-seed", cachedCred.AccessKeyID)
	})

	t.Run("ProviderErrorIsIgnoredIfSeeded", func(t *testing.T) {
		resetCredsCacheForTest()

		seedCache(creds(true))
		p := WithSeed(&fakeProvider{err: errors.New("Uh-oh")}, cacheKey, minimumCachedCredentialLifetime)

		c, err := p.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test-seed", c.AccessKeyID)
	})

	t.Run("ProviderErrorIfNoSeed", func(t *testing.T) {
		resetCredsCacheForTest()

		p := WithSeed(&fakeProvider{err: errors.New("Uh-oh")}, cacheKey, minimumCachedCredentialLifetime)

		_, err := p.Retrieve(t.Context())
		require.ErrorContains(t, err, "Uh-oh")
	})
}

func TestCreateAssumeRoleCacheKey(t *testing.T) {
	t.Run("CreatesCorrectCacheKey", func(t *testing.T) {
		assert.Equal(t, "role", createAssumeRoleCacheKey("role", nil))
	})

	t.Run("CreatesCacheKeyWithExternalID", func(t *testing.T) {
		assert.Equal(t, "role|x", createAssumeRoleCacheKey("role", utility.ToStringPtr("x")))
	})
}
