package testutil

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/pkg/errors"
)

func CleanupS3Bucket(ctx context.Context, creds aws.CredentialsProvider, name, prefix, region string) error {
	svc, err := CreateS3Client(creds, region)
	if err != nil {
		return errors.Wrap(err, "creating S3 client")
	}
	deleteObjectsInput := &s3.DeleteObjectsInput{
		Bucket: aws.String(name),
		Delete: &s3Types.Delete{},
	}
	listInput := &s3.ListObjectsInput{
		Bucket: aws.String(name),
		Prefix: aws.String(prefix),
	}
	var result *s3.ListObjectsOutput

	for {
		result, err = svc.ListObjects(ctx, listInput)
		if err != nil {
			return errors.Wrap(err, "listing objects")
		}

		for _, object := range result.Contents {
			deleteObjectsInput.Delete.Objects = append(deleteObjectsInput.Delete.Objects, s3Types.ObjectIdentifier{
				Key: object.Key,
			})
		}

		if deleteObjectsInput.Delete.Objects != nil {
			_, err = svc.DeleteObjects(ctx, deleteObjectsInput)
			if err != nil {
				return errors.Wrap(err, "deleting S3 bucket objects")
			}
			deleteObjectsInput.Delete = &s3Types.Delete{}
		}

		if *result.IsTruncated {
			listInput.Marker = result.Contents[len(result.Contents)-1].Key
		} else {
			break
		}
	}

	return nil
}

func CreateS3Client(creds aws.CredentialsProvider, region string) (*s3.Client, error) {
	// kim: TODO: remove
	// sess, err := session.NewSession(&aws.Config{
	//     Credentials: creds,
	//     Region:      aws.String(region),
	// })
	// if err != nil {
	//     return nil, errors.Wrap(err, "problem connecting to AWS")
	// }
	// svc := s3.New(sess)
	// return svc, nil
	svc := s3.New(s3.Options{
		Credentials: creds,
		Region:      region,
	})
	return svc, nil
}
