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

var minimumCachedCredentialLifetime = 5 * time.Minute

var credsCacheMutex sync.Mutex
var credsCache = map[string]*aws.Credentials{}

type seededCredentialProvider struct {
	provider aws.CredentialsProvider

	cacheKey string
}

func (s *seededCredentialProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	credsCacheMutex.Lock()
	defer credsCacheMutex.Unlock()

	if cachedCreds, ok := credsCache[s.cacheKey]; ok {
		if time.Now().Before(cachedCreds.Expires.Add(-minimumCachedCredentialLifetime)) {
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
func WithSeed(provider aws.CredentialsProvider, cacheKey string) aws.CredentialsProvider {
	return &seededCredentialProvider{
		provider: provider,
		cacheKey: cacheKey,
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
