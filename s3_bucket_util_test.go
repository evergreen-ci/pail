package pail

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3Manager "github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/evergreen-ci/pail/testutil"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getS3SmallBucketTests(ctx context.Context, tempdir string, s3Credentials aws.CredentialsProvider, s3BucketName, s3Prefix, s3Region string) []bucketTestCase {
	return []bucketTestCase{
		{
			id: "VerifyBucketType",
			test: func(t *testing.T, b Bucket) {
				bucket, ok := b.(*s3BucketSmall)
				require.True(t, ok)
				assert.NotNil(t, bucket)
			},
		},
		{
			id: "TestCredentialsOverrideDefaults",
			test: func(t *testing.T, b Bucket) {
				input := &s3.GetBucketLocationInput{
					Bucket: aws.String(s3BucketName),
				}

				rawBucket := b.(*s3BucketSmall)
				_, err := rawBucket.svc.GetBucketLocation(ctx, input)
				assert.NoError(t, err)

				badOptions := S3Options{
					Credentials: CreateAWSStaticCredentials("asdf", "asdf", "asdf"),
					Region:      s3Region,
					Name:        s3BucketName,
				}
				badBucket, err := NewS3Bucket(ctx, badOptions)
				require.NoError(t, err)
				rawBucket = badBucket.(*s3BucketSmall)
				_, err = rawBucket.svc.GetBucketLocation(ctx, input)
				assert.Error(t, err)
			},
		},
		{
			id: "TestCheckPassesWhenDoNotHaveAccess",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketSmall)
				rawBucket.name = "mciuploads"
				assert.NoError(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestCheckFailsWhenBucketDNE",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketSmall)
				rawBucket.name = testutil.NewUUID()
				assert.Error(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsOption",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				newFile, err := os.Create(filepath.Join(tempdir, "creds"))
				require.NoError(t, err)
				defer newFile.Close()
				_, err = newFile.WriteString("[my_profile]\n")
				require.NoError(t, err)
				awsKey := fmt.Sprintf("aws_access_key_id = %s\n", os.Getenv("AWS_KEY"))
				_, err = newFile.WriteString(awsKey)
				require.NoError(t, err)
				awsSecret := fmt.Sprintf("aws_secret_access_key = %s\n", os.Getenv("AWS_SECRET"))
				_, err = newFile.WriteString(awsSecret)
				require.NoError(t, err)

				sharedCredsOptions := S3Options{
					SharedCredentialsFilepath: filepath.Join(tempdir, "creds"),
					SharedCredentialsProfile:  "my_profile",
					Region:                    s3Region,
					Name:                      s3BucketName,
				}
				sharedCredsBucket, err := NewS3Bucket(ctx, sharedCredsOptions)
				require.NoError(t, err)
				assert.NoError(t, sharedCredsBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsUsesCorrectDefaultFile",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				homeDir, err := homedir.Dir()
				require.NoError(t, err)
				fileName := filepath.Join(homeDir, ".aws", "credentials")

				if _, err = os.Stat(fileName); os.IsNotExist(err) {
					t.Skip("static credentials file not present")
				}
				require.NoError(t, b.Check(ctx))

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "default",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				sharedCredsBucket, err := NewS3Bucket(ctx, sharedCredsOptions)
				require.NoError(t, err)
				assert.NoError(t, sharedCredsBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsFailsWhenProfileDNE",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "DNE",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				sharedCredsBucket, err := NewS3Bucket(ctx, sharedCredsOptions)
				assert.Error(t, err)
				assert.Zero(t, sharedCredsBucket)
			},
		},
		{
			id: "TestPermissions",
			test: func(t *testing.T, b Bucket) {
				// default permissions
				key1 := testutil.NewUUID()
				writer, err := b.Writer(ctx, key1)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketSmall)
				objectACLInput := &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key1)),
				}
				objectACLOutput, err := rawBucket.svc.GetObjectAcl(ctx, objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 1, len(objectACLOutput.Grants))
				assert.Equal(t, s3Types.PermissionFullControl, objectACLOutput.Grants[0].Permission)

				// explicitly set permissions
				openOptions := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + testutil.NewUUID(),
					Permissions: S3PermissionsPublicRead,
				}
				openBucket, err := NewS3Bucket(ctx, openOptions)
				require.NoError(t, err)
				key2 := testutil.NewUUID()
				writer, err = openBucket.Writer(ctx, key2)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = openBucket.(*s3BucketSmall)
				objectACLInput = &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key2)),
				}
				objectACLOutput, err = rawBucket.svc.GetObjectAcl(ctx, objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, s3Types.PermissionRead, objectACLOutput.Grants[1].Permission)

				// copy with permissions
				destKey := testutil.NewUUID()
				copyOpts := CopyOptions{
					SourceKey:         key1,
					DestinationKey:    destKey,
					DestinationBucket: openBucket,
				}
				require.NoError(t, b.Copy(ctx, copyOpts))
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, s3Types.PermissionRead, objectACLOutput.Grants[1].Permission)
			},
		},
		{
			id: "TestContentType",
			test: func(t *testing.T, b Bucket) {
				// default content type
				key := testutil.NewUUID()
				writer, err := b.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketSmall)
				getObjectInput := &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err := rawBucket.svc.GetObject(ctx, getObjectInput)
				require.NoError(t, err)
				assert.Equal(t, "application/octet-stream", aws.ToString(getObjectOutput.ContentType))

				// explicitly set content type
				htmlOptions := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + testutil.NewUUID(),
					ContentType: "html/text",
				}
				htmlBucket, err := NewS3Bucket(ctx, htmlOptions)
				require.NoError(t, err)
				key = testutil.NewUUID()
				writer, err = htmlBucket.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = htmlBucket.(*s3BucketSmall)
				getObjectInput = &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err = rawBucket.svc.GetObject(ctx, getObjectInput)
				require.NoError(t, err)
				require.NotNil(t, getObjectOutput.ContentType)
				assert.Equal(t, "html/text", *getObjectOutput.ContentType)
			},
		},
		{
			id: "TestIfNotExists",
			test: func(t *testing.T, b Bucket) {
				key := testutil.NewUUID()
				ifNotExistsOptions := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + testutil.NewUUID(),
					IfNotExists: true,
				}

				ifNotExistsBucket, err := NewS3Bucket(ctx, ifNotExistsOptions)
				require.NoError(t, err)
				writer, err := ifNotExistsBucket.Writer(ctx, key)
				require.NoError(t, err)

				payload := []byte("hello world")

				_, err = writer.Write(payload)
				require.NoError(t, err)
				require.NoError(t, writer.Close())

				_, err = writer.Write([]byte("hello world 2"))
				require.NoError(t, err)
				require.Error(t, writer.Close())

				rawBucket := ifNotExistsBucket.(*s3BucketSmall)
				getObjectInput := &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err := rawBucket.svc.GetObject(ctx, getObjectInput)
				require.NoError(t, err)
				content, err := io.ReadAll(getObjectOutput.Body)
				require.NoError(t, err)

				assert.Equal(t, payload, content)
			},
		},
		{
			id: "TestCompressingWriter",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketSmall)
				s3Options := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      rawBucket.prefix,
					MaxRetries:  aws.Int(20),
					Compress:    true,
				}
				cb, err := NewS3Bucket(ctx, s3Options)
				require.NoError(t, err)

				data := []byte{}
				for i := 0; i < 300; i++ {
					data = append(data, []byte(testutil.NewUUID())...)
				}

				uncompressedKey := testutil.NewUUID()
				w, err := b.Writer(ctx, uncompressedKey)
				require.NoError(t, err)
				n, err := w.Write(data)
				require.NoError(t, err)
				require.NoError(t, w.Close())
				assert.Equal(t, len(data), n)

				compressedKey := testutil.NewUUID()
				cw, err := cb.Writer(ctx, compressedKey)
				require.NoError(t, err)
				n, err = cw.Write(data)
				require.NoError(t, err)
				require.NoError(t, cw.Close())
				assert.Equal(t, len(data), n)
				compressedData := cw.(*compressingWriteCloser).s3Writer.(*smallWriteCloser).buffer

				reader, err := gzip.NewReader(bytes.NewReader(compressedData))
				require.NoError(t, err)
				decompressedData, err := ioutil.ReadAll(reader)
				require.NoError(t, reader.Close())
				require.NoError(t, err)
				assert.Equal(t, data, decompressedData)

				cr, err := cb.Get(ctx, compressedKey)
				require.NoError(t, err)
				s3CompressedData, err := ioutil.ReadAll(cr)
				require.NoError(t, err)
				require.NoError(t, cr.Close())
				assert.Equal(t, data, s3CompressedData)

				r, err := cb.Get(ctx, uncompressedKey)
				require.NoError(t, err)
				s3UncompressedData, err := ioutil.ReadAll(r)
				require.NoError(t, err)
				require.NoError(t, r.Close())
				assert.Equal(t, data, s3UncompressedData)
			},
		},
		{
			id: "TestCompressingPut",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketSmall)
				s3Options := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      rawBucket.prefix,
					MaxRetries:  aws.Int(20),
					Compress:    true,
				}
				client := &http.Client{}
				cb, err := NewFastGetS3BucketWithHTTPClient(ctx, client, s3Options)
				require.NoError(t, err)

				data := []byte{}
				for i := 0; i < 300; i++ {
					data = append(data, []byte(testutil.NewUUID())...)
				}

				compressedKey := testutil.NewUUID()
				require.NoError(t, cb.Put(ctx, compressedKey, bytes.NewReader(data)))

				buf := s3Manager.NewWriteAtBuffer([]byte{})
				require.NoError(t, cb.GetToWriter(ctx, compressedKey, buf))

				s3CompressedData, err := io.ReadAll(bytes.NewReader(buf.Bytes()))
				require.NoError(t, err)

				gzr, err := gzip.NewReader(bytes.NewReader(s3CompressedData))
				require.NoError(t, err)
				gotData, err := io.ReadAll(gzr)
				require.NoError(t, err)
				require.NoError(t, gzr.Close())

				assert.Equal(t, data, gotData)
			},
		},
		{
			id:   "PullWithCache",
			test: makePullWithCacheTest(ctx, tempdir),
		},
	}
}

func getS3LargeBucketTests(ctx context.Context, tempdir string, s3Credentials aws.CredentialsProvider, s3BucketName, s3Prefix, s3Region string) []bucketTestCase {
	return []bucketTestCase{
		{
			id: "VerifyBucketType",
			test: func(t *testing.T, b Bucket) {
				bucket, ok := b.(*s3BucketLarge)
				require.True(t, ok)
				assert.NotNil(t, bucket)
			},
		},
		{
			id: "TestCredentialsOverrideDefaults",
			test: func(t *testing.T, b Bucket) {
				input := &s3.GetBucketLocationInput{
					Bucket: aws.String(s3BucketName),
				}

				rawBucket := b.(*s3BucketLarge)
				_, err := rawBucket.svc.GetBucketLocation(ctx, input)
				assert.NoError(t, err)

				badOptions := S3Options{
					Credentials: CreateAWSStaticCredentials("asdf", "asdf", "asdf"),
					Region:      s3Region,
					Name:        s3BucketName,
				}
				badBucket, err := NewS3MultiPartBucket(ctx, badOptions)
				require.NoError(t, err)
				rawBucket = badBucket.(*s3BucketLarge)
				_, err = rawBucket.svc.GetBucketLocation(ctx, input)
				assert.Error(t, err)
			},
		},
		{
			id: "TestCheckPassesWhenDoNotHaveAccess",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketLarge)
				rawBucket.name = "mciuploads"
				assert.NoError(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestCheckFailsWhenBucketDNE",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketLarge)
				rawBucket.name = testutil.NewUUID()
				assert.Error(t, rawBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsOption",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				newFile, err := os.Create(filepath.Join(tempdir, "creds"))
				require.NoError(t, err)
				defer newFile.Close()
				_, err = newFile.WriteString("[my_profile]\n")
				require.NoError(t, err)
				awsKey := fmt.Sprintf("aws_access_key_id = %s\n", os.Getenv("AWS_KEY"))
				_, err = newFile.WriteString(awsKey)
				require.NoError(t, err)
				awsSecret := fmt.Sprintf("aws_secret_access_key = %s\n", os.Getenv("AWS_SECRET"))
				_, err = newFile.WriteString(awsSecret)
				require.NoError(t, err)

				sharedCredsOptions := S3Options{
					SharedCredentialsFilepath: filepath.Join(tempdir, "creds"),
					SharedCredentialsProfile:  "my_profile",
					Region:                    s3Region,
					Name:                      s3BucketName,
				}
				sharedCredsBucket, err := NewS3MultiPartBucket(ctx, sharedCredsOptions)
				require.NoError(t, err)
				assert.NoError(t, sharedCredsBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsUsesCorrectDefaultFile",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				homeDir, err := homedir.Dir()
				require.NoError(t, err)
				fileName := filepath.Join(homeDir, ".aws", "credentials")

				if _, err = os.Stat(fileName); os.IsNotExist(err) {
					t.Skip("static credentials file not present")
				}

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "default",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				sharedCredsBucket, err := NewS3MultiPartBucket(ctx, sharedCredsOptions)
				require.NoError(t, err)
				assert.NoError(t, sharedCredsBucket.Check(ctx))
			},
		},
		{
			id: "TestSharedCredentialsFailsWhenProfileDNE",
			test: func(t *testing.T, b Bucket) {
				require.NoError(t, b.Check(ctx))

				sharedCredsOptions := S3Options{
					SharedCredentialsProfile: "DNE",
					Region:                   s3Region,
					Name:                     s3BucketName,
				}
				sharedCredsBucket, err := NewS3MultiPartBucket(ctx, sharedCredsOptions)
				assert.Error(t, err)
				assert.Zero(t, sharedCredsBucket)
			},
		},
		{
			id: "TestPermissions",
			test: func(t *testing.T, b Bucket) {
				// default permissions
				key1 := testutil.NewUUID()
				writer, err := b.Writer(ctx, key1)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketLarge)
				objectACLInput := &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key1)),
				}
				objectACLOutput, err := rawBucket.svc.GetObjectAcl(ctx, objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 1, len(objectACLOutput.Grants))
				assert.Equal(t, s3Types.PermissionFullControl, objectACLOutput.Grants[0].Permission)

				// explicitly set permissions
				openOptions := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + testutil.NewUUID(),
					Permissions: S3PermissionsPublicRead,
				}
				openBucket, err := NewS3MultiPartBucket(ctx, openOptions)
				require.NoError(t, err)
				key2 := testutil.NewUUID()
				writer, err = openBucket.Writer(ctx, key2)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = openBucket.(*s3BucketLarge)
				objectACLInput = &s3.GetObjectAclInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key2)),
				}
				objectACLOutput, err = rawBucket.svc.GetObjectAcl(ctx, objectACLInput)
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, s3Types.PermissionRead, objectACLOutput.Grants[1].Permission)

				// copy with permissions
				destKey := testutil.NewUUID()
				copyOpts := CopyOptions{
					SourceKey:         key1,
					DestinationKey:    destKey,
					DestinationBucket: openBucket,
				}
				require.NoError(t, b.Copy(ctx, copyOpts))
				require.NoError(t, err)
				require.Equal(t, 2, len(objectACLOutput.Grants))
				assert.Equal(t, s3Types.PermissionRead, objectACLOutput.Grants[1].Permission)
			},
		},
		{
			id: "TestLargeFileRoundTrip",
			test: func(t *testing.T, b Bucket) {
				size := int64(10000000)
				key := testutil.NewUUID()
				bigBuff := make([]byte, size)
				path := filepath.Join(tempdir, "bigfile.test0")

				// upload large empty file
				require.NoError(t, ioutil.WriteFile(path, bigBuff, 0666))
				require.NoError(t, b.Upload(ctx, key, path))

				// check size of empty file
				path = filepath.Join(tempdir, "bigfile.test1")
				require.NoError(t, b.Download(ctx, key, path))
				fi, err := os.Stat(path)
				require.NoError(t, err)
				assert.Equal(t, size, fi.Size())
			},
		},

		{
			id: "TestContentType",
			test: func(t *testing.T, b Bucket) {
				// default content type
				key := testutil.NewUUID()
				writer, err := b.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket := b.(*s3BucketLarge)
				getObjectInput := &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err := rawBucket.svc.GetObject(ctx, getObjectInput)
				require.NoError(t, err)
				assert.Equal(t, "binary/octet-stream", aws.ToString(getObjectOutput.ContentType))

				// explicitly set content type
				htmlOptions := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + testutil.NewUUID(),
					ContentType: "html/text",
				}
				htmlBucket, err := NewS3MultiPartBucket(ctx, htmlOptions)
				require.NoError(t, err)
				key = testutil.NewUUID()
				writer, err = htmlBucket.Writer(ctx, key)
				require.NoError(t, err)
				_, err = writer.Write([]byte("hello world"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				rawBucket = htmlBucket.(*s3BucketLarge)
				getObjectInput = &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err = rawBucket.svc.GetObject(ctx, getObjectInput)
				require.NoError(t, err)
				assert.Equal(t, "html/text", aws.ToString(getObjectOutput.ContentType))
			},
		},
		{
			id: "TestIfNotExists",
			test: func(t *testing.T, b Bucket) {
				key := testutil.NewUUID()
				ifNotExistsOptions := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      s3Prefix + testutil.NewUUID(),
					IfNotExists: true,
				}

				ifNotExistsBucket, err := NewS3MultiPartBucket(ctx, ifNotExistsOptions)
				require.NoError(t, err)
				writer, err := ifNotExistsBucket.Writer(ctx, key)
				require.NoError(t, err)

				payload := []byte("hello world")

				_, err = writer.Write(payload)
				require.NoError(t, err)
				require.NoError(t, writer.Close())

				_, err = writer.Write([]byte("hello world 2"))
				require.NoError(t, err)
				require.Error(t, writer.Close())

				rawBucket := ifNotExistsBucket.(*s3BucketLarge)
				getObjectInput := &s3.GetObjectInput{
					Bucket: aws.String(s3BucketName),
					Key:    aws.String(rawBucket.normalizeKey(key)),
				}
				getObjectOutput, err := rawBucket.svc.GetObject(ctx, getObjectInput)
				require.NoError(t, err)
				content, err := io.ReadAll(getObjectOutput.Body)
				require.NoError(t, err)

				assert.Equal(t, payload, content)
			},
		},
		{
			id: "TestGetToWriter",
			test: func(t *testing.T, b Bucket) {
				s3b, ok := b.(FastGetS3Bucket)
				assert.Equal(t, true, ok)

				key := testutil.NewUUID()

				payload := []byte("hello world")
				ctx := t.Context()

				err := s3b.Put(ctx, key, bytes.NewReader(payload))
				require.NoError(t, err)

				localPath := filepath.Join(tempdir, key)

				require.NoError(t, os.MkdirAll(localPath, 0700))
				f, err := os.CreateTemp(localPath, "get-to-writer")
				require.NoError(t, err)

				t.Cleanup(func() {
					os.RemoveAll(localPath)
				})

				err = s3b.GetToWriter(ctx, key, f)
				require.NoError(t, err)

				got, err := os.ReadFile(f.Name())
				require.NoError(t, err)

				assert.Equal(t, payload, got)
			},
		},
		{
			id: "TestCompressingWriter",
			test: func(t *testing.T, b Bucket) {
				rawBucket := b.(*s3BucketLarge)
				s3Options := S3Options{
					Credentials: s3Credentials,
					Region:      s3Region,
					Name:        s3BucketName,
					Prefix:      rawBucket.prefix,
					MaxRetries:  aws.Int(20),
					Compress:    true,
				}
				cb, err := NewS3MultiPartBucket(ctx, s3Options)
				require.NoError(t, err)

				data := []byte{}
				for i := 0; i < 300; i++ {
					data = append(data, []byte(testutil.NewUUID())...)
				}

				uncompressedKey := testutil.NewUUID()
				w, err := b.Writer(ctx, uncompressedKey)
				require.NoError(t, err)
				n, err := w.Write(data)
				require.NoError(t, err)
				require.NoError(t, w.Close())
				assert.Equal(t, len(data), n)

				compressedKey := testutil.NewUUID()
				cw, err := cb.Writer(ctx, compressedKey)
				require.NoError(t, err)
				n, err = cw.Write(data)
				require.NoError(t, err)
				require.NoError(t, cw.Close())
				assert.Equal(t, len(data), n)
				_, ok := cw.(*compressingWriteCloser).s3Writer.(*largeWriteCloser)
				assert.True(t, ok)

				cr, err := cb.Get(ctx, compressedKey)
				require.NoError(t, err)
				s3CompressedData, err := ioutil.ReadAll(cr)
				require.NoError(t, err)
				require.NoError(t, cr.Close())
				assert.Equal(t, data, s3CompressedData)

				r, err := cb.Get(ctx, uncompressedKey)
				require.NoError(t, err)
				s3UncompressedData, err := ioutil.ReadAll(r)
				require.NoError(t, err)
				require.NoError(t, r.Close())
				assert.Equal(t, data, s3UncompressedData)
			},
		},
		// The test below should be enabled once we have changed how we handle file hash
		// comparisons. Currently multi-part uploads always fail this test.
		// {
		// 	id:   "PullWithCache",
		// 	test: makePullWithCacheTest(ctx, tempdir),
		// },
	}
}

func makePullWithCacheTest(ctx context.Context, tempdir string) func(*testing.T, Bucket) {
	return func(t *testing.T, bucket Bucket) {
		prefix := testutil.NewUUID()
		localPath := filepath.Join(tempdir, prefix)

		require.NoError(t, os.MkdirAll(localPath, 0700))

		f, err := os.CreateTemp(localPath, "pull-with-cache")
		require.NoError(t, err)

		_, err = io.Copy(f, bytes.NewReader([]byte("test-content")))
		require.NoError(t, err)

		initialInfo, err := f.Stat()
		require.NoError(t, err)

		require.NoError(t, bucket.Push(ctx, SyncOptions{Local: localPath, Remote: prefix}))
		iter, err := bucket.List(ctx, prefix)
		require.NoError(t, err)
		counter := 0
		filenames := map[string]bool{f.Name(): true}
		for iter.Next(ctx) {
			name := filepath.Join(tempdir, iter.Item().Name())
			require.True(t, filenames[name])
			counter++
		}
		require.NoError(t, iter.Err())
		require.Equal(t, 1, counter)

		require.NoError(t, bucket.Pull(ctx, SyncOptions{Local: localPath, Remote: prefix}))

		finalInfo, err := f.Stat()
		require.NoError(t, err)

		assert.True(t, finalInfo.ModTime().Equal(initialInfo.ModTime()))
	}
}
