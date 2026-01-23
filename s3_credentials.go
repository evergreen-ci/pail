package pail

import (
	"context"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

var credsCacheMutex sync.Mutex
var credsCache = map[string]*aws.Credentials{}

type seededCredentialProvider struct {
	provider aws.CredentialsProvider

	cacheKey string
	// We use minimumLifetime rather than implementing aws's AdjustExpiresBy interface
	// because our cache can be shared across multiple aws clients with different expiry
	// adjustment needs. The AdjustExpiresBy interface is only called when setting new
	// credentials in the cache, so this approach allows us to have per-client expiry
	// adjustment needs.
	minimumLifetime time.Duration
}

// Retrieve fetches the latest credentials from the cache or underlying provider.
func (s *seededCredentialProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	credsCacheMutex.Lock()
	defer credsCacheMutex.Unlock()

	if cachedCreds, ok := credsCache[s.cacheKey]; ok {
		if time.Now().Before(cachedCreds.Expires.Add(-s.minimumLifetime)) {
			return *cachedCreds, nil
		}
		delete(credsCache, s.cacheKey)
	}

	creds, err := s.provider.Retrieve(ctx)
	if err != nil {
		return creds, err
	}

	credsCache[s.cacheKey] = &creds
	return creds, nil
}

// WithSeed wraps the given CredentialsProvider with a caching layer that uses
// the given cacheKey to identify cached credentials.
func WithSeed(provider aws.CredentialsProvider, cacheKey string, minimumLifetime time.Duration) aws.CredentialsProvider {
	return &seededCredentialProvider{
		provider:        provider,
		cacheKey:        cacheKey,
		minimumLifetime: minimumLifetime,
	}
}

// CreateAWSStaticCredentials is a wrapper for creating static AWS credentials.
func CreateAWSStaticCredentials(awsKey, awsPassword, awsToken string) aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider(awsKey, awsPassword, awsToken)
}

// CreateAWSAssumeRoleCredentials creates an AWS CredentialsProvider that
// assumes the given role ARN using the provided STS client. An optional external
// ID can be provided to further secure the assume role operation.
func CreateAWSAssumeRoleCredentials(client *sts.Client, roleARN string, externalID *string) aws.CredentialsProvider {
	return stscreds.NewAssumeRoleProvider(client, roleARN, func(aro *stscreds.AssumeRoleOptions) {
		aro.ExternalID = externalID
	})
}

// createAssumeRoleCacheKey generates a unique cache key for the given role ARN
// and external ID combination.
func createAssumeRoleCacheKey(roleARN string, externalID *string) string {
	if externalID != nil {
		return roleARN + "|" + *externalID
	}
	return roleARN
}
