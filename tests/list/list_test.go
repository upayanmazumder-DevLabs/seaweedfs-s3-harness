// Package list covers ListObjectsV2 pagination, delimiters, consistency and
// multipart-upload listing: matrix IDs L01-L08 in docs/test-matrix.md.
package list

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

// L01: paginating with a small MaxKeys must eventually enumerate every key,
// with no duplicates and no gaps.
func TestListPaginationCompleteness(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "list")
	prefix := testkit.Prefix(t)

	const n = 130
	want := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("%s%04d", prefix, i)
		if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x"))}); err != nil {
			t.Fatalf("PutObject(%d): %v", i, err)
		}
		want[key] = true
	}

	for _, pageSize := range []int32{1, 7, 1000} {
		pageSize := pageSize
		t.Run(fmt.Sprintf("maxKeys=%d", pageSize), func(t *testing.T) {
			seen := map[string]int{}
			var token *string
			pages := 0
			for {
				out, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
					Bucket: aws.String(bucket), Prefix: aws.String(prefix),
					MaxKeys: aws.Int32(pageSize), ContinuationToken: token,
				})
				if err != nil {
					t.Fatalf("ListObjectsV2: %v", err)
				}
				pages++
				if pages > n+5 {
					t.Fatal("pagination did not terminate within a sane number of pages")
				}
				for _, o := range out.Contents {
					seen[aws.ToString(o.Key)]++
				}
				if out.IsTruncated == nil || !*out.IsTruncated {
					break
				}
				token = out.NextContinuationToken
			}
			for k := range want {
				if seen[k] != 1 {
					t.Errorf("key %s: seen %d times across pagination, want exactly 1", k, seen[k])
				}
			}
			if len(seen) != len(want) {
				t.Errorf("total distinct keys seen: want %d got %d", len(want), len(seen))
			}
		})
	}
}

// L02: a delimiter of "/" must group keys into CommonPrefixes and exclude
// them from Contents.
func TestDelimiterGrouping(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "list")
	prefix := testkit.Prefix(t)

	keys := []string{"a", "dir1/x", "dir1/y", "dir2/z"}
	for _, k := range keys {
		if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(prefix + k), Body: bytes.NewReader([]byte("x"))}); err != nil {
			t.Fatalf("PutObject(%s): %v", k, err)
		}
	}

	out, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/"),
	})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}

	if len(out.Contents) != 1 || aws.ToString(out.Contents[0].Key) != prefix+"a" {
		t.Errorf("Contents: want exactly [%s], got %v", prefix+"a", out.Contents)
	}
	wantPrefixes := map[string]bool{prefix + "dir1/": true, prefix + "dir2/": true}
	if len(out.CommonPrefixes) != len(wantPrefixes) {
		t.Fatalf("CommonPrefixes: want %d got %d (%v)", len(wantPrefixes), len(out.CommonPrefixes), out.CommonPrefixes)
	}
	for _, cp := range out.CommonPrefixes {
		if !wantPrefixes[aws.ToString(cp.Prefix)] {
			t.Errorf("unexpected CommonPrefix %s", aws.ToString(cp.Prefix))
		}
	}
}

// L05: a write must be visible to a listing issued immediately after, and a
// delete must be absent from a listing issued immediately after - the
// strong-consistency claim this harness depends on everywhere else.
func TestReadAfterWriteConsistency(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "list")
	key := testkit.Prefix(t) + "raw"

	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x"))}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	out, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(key)})
	if err != nil {
		t.Fatalf("ListObjectsV2 after write: %v", err)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("key not visible immediately after PutObject (list-after-write)")
	}

	if _, err := cl.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	out2, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(key)})
	if err != nil {
		t.Fatalf("ListObjectsV2 after delete: %v", err)
	}
	if len(out2.Contents) != 0 {
		t.Fatalf("key still visible immediately after DeleteObject (list-after-delete)")
	}
}
