package pail

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalkTree(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	ctx := context.Background()

	t.Run("CanceledContext", func(t *testing.T) {
		tctx, cancel := context.WithCancel(ctx)
		cancel()
		out, err := walkLocalTree(tctx, filepath.Dir(file))
		assert.Error(t, err)
		assert.Nil(t, out)
	})
	t.Run("MissingPath", func(t *testing.T) {
		out, err := walkLocalTree(ctx, "")
		assert.NoError(t, err)
		assert.Nil(t, out)
	})
	t.Run("WorkingExample", func(t *testing.T) {
		out, err := walkLocalTree(ctx, filepath.Dir(file))
		assert.NoError(t, err)
		assert.NotNil(t, out)
	})
}

func TestTarFile(t *testing.T) {
	for testName, testCase := range map[string]func(t *testing.T, dir string){
		"CreatesTarWithSingleFile": func(t *testing.T, dir string) {
			fileName := "foo.txt"
			fileContent := "bar"
			require.NoError(t, ioutil.WriteFile(filepath.Join(dir, fileName), []byte(fileContent), 0777))

			b := &bytes.Buffer{}
			tw := tar.NewWriter(b)
			require.NoError(t, tarFile(tw, dir, fileName))

			tr := tar.NewReader(b)
			header, err := tr.Next()
			require.NoError(t, err)
			assert.Equal(t, fileName, header.Name)
			assert.EqualValues(t, tar.TypeReg, header.Typeflag)
			checkContent := &bytes.Buffer{}
			_, err = io.Copy(checkContent, tr)
			require.NoError(t, err)
			assert.Equal(t, fileContent, checkContent.String())

			_, err = tr.Next()
			assert.Equal(t, io.EOF, err)
		},
		"CreatesTarWithDirectory": func(t *testing.T, dir string) {
			subDirName := "foo"
			absPath := filepath.Join(dir, subDirName)
			require.NoError(t, os.Mkdir(absPath, 0777))

			b := &bytes.Buffer{}
			tw := tar.NewWriter(b)
			require.NoError(t, tarFile(tw, dir, subDirName))

			tr := tar.NewReader(b)
			header, err := tr.Next()
			require.NoError(t, err)
			assert.Equal(t, subDirName+"/", header.Name)
			assert.EqualValues(t, tar.TypeDir, header.Typeflag)
		},
		"CreatesTarWithFilesInSubdirectory": func(t *testing.T, dir string) {
			relFilePath := filepath.Join("foo", "bar.txt")
			absPath := filepath.Join(dir, relFilePath)
			require.NoError(t, os.MkdirAll(filepath.Dir(absPath), 0777))
			fileContent := []byte("bat")
			require.NoError(t, ioutil.WriteFile(absPath, fileContent, 0777))

			b := &bytes.Buffer{}
			tw := tar.NewWriter(b)
			require.NoError(t, tarFile(tw, dir, relFilePath))

			tr := tar.NewReader(b)
			header, err := tr.Next()
			require.NoError(t, err)
			assert.Equal(t, filepath.ToSlash(relFilePath), header.Name)
			assert.EqualValues(t, tar.TypeReg, header.Typeflag)
			checkContent, err := ioutil.ReadAll(tr)
			require.NoError(t, err)
			assert.Equal(t, fileContent, checkContent)
		},
		"FailsForFileNotWithinBaseDirectory": func(t *testing.T, dir string) {
			tmpFile, err := ioutil.TempFile("", "outside_tar_dir")
			require.NoError(t, err)
			require.NoError(t, tmpFile.Close())

			b := &bytes.Buffer{}
			tw := tar.NewWriter(b)
			assert.Error(t, tarFile(tw, dir, tmpFile.Name()))
		},
		"FailsForNonexistentFile": func(t *testing.T, dir string) {
			b := &bytes.Buffer{}
			tw := tar.NewWriter(b)
			assert.Error(t, tarFile(tw, dir, "nonexistent_file"))
		},
	} {
		t.Run(testName, func(t *testing.T) {
			tmpDir, err := ioutil.TempDir("", "tar_file_test")
			require.NoError(t, err)
			testCase(t, tmpDir)
		})
	}
}

func TestEscapeCopySource(t *testing.T) {
	for testName, tc := range map[string]struct {
		input    string
		expected string
	}{
		"EmptyStringReturnsEmpty": {
			input:    "",
			expected: "",
		},
		"PlainAlphanumericUnchanged": {
			input:    "mybucket/project/task/0/task_logs/agent/chunk",
			expected: "mybucket/project/task/0/task_logs/agent/chunk",
		},
		// '+' must be encoded as '%2B': S3 applies form-encoding semantics to the
		// CopySource header and interprets a literal '+' as a space, causing NoSuchKey
		// for objects whose keys contain a literal '+'.
		"PlusSignEncodedAsPercent2B": {
			input:    "mybucket/myproject/task_+_variant/0/task_logs/system/chunk",
			expected: "mybucket/myproject/task_%2B_variant/0/task_logs/system/chunk",
		},
		"ForwardSlashNotEncoded": {
			input:    "mybucket/prefix/key/with/slashes",
			expected: "mybucket/prefix/key/with/slashes",
		},
		"TildeNotEncoded": {
			input:    "mybucket/key~with~tildes",
			expected: "mybucket/key~with~tildes",
		},
		"HyphenAndUnderscoreNotEncoded": {
			input:    "mybucket/key-with_hyphens_and-underscores",
			expected: "mybucket/key-with_hyphens_and-underscores",
		},
		"SpaceEncoded": {
			input:    "mybucket/key with spaces",
			expected: "mybucket/key%20with%20spaces",
		},
		"TabEncoded": {
			input:    "mybucket/key\twith\ttabs",
			expected: "mybucket/key%09with%09tabs",
		},
		"NewlineEncoded": {
			input:    "mybucket/key\nwith\nnewlines",
			expected: "mybucket/key%0Awith%0Anewlines",
		},
		"CarriageReturnEncoded": {
			input:    "mybucket/key\rwith\rCR",
			expected: "mybucket/key%0Dwith%0DCR",
		},
		"NullByteEncoded": {
			input:    "mybucket/key\x00with\x00nulls",
			expected: "mybucket/key%00with%00nulls",
		},
		"DELEncoded": {
			input:    "mybucket/key\x7fwith\x7fdel",
			expected: "mybucket/key%7Fwith%7Fdel",
		},
		"NonASCIIEncoded": {
			input:    "mybucket/key\x80\xFF",
			expected: "mybucket/key%80%FF",
		},
		// Realistic key path: long task ID containing both '+' and '~' with multiple
		// path segments, matching the format used for task log chunk keys.
		// '+' is encoded; '~' (RFC 3986 unreserved) is left as-is.
		"LongTaskKeyPathWithPlusAndTilde": {
			input:    "mybucket/myproject/mytask_+_myvariant__param~value/0/task_logs/system/0_100_200_50_300",
			expected: "mybucket/myproject/mytask_%2B_myvariant__param~value/0/task_logs/system/0_100_200_50_300",
		},
	} {
		t.Run(testName, func(t *testing.T) {
			assert.Equal(t, tc.expected, escapeCopySource(tc.input))
		})
	}
}
