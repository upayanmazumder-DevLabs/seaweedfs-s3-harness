package testkit

import (
	"crypto/md5"
	"fmt"
)

// MultipartETag computes the AWS-compatible ETag for a completed multipart
// upload from the plain MD5 digest (16 raw bytes, not hex) of each part, in
// part order: hex(md5(concat(partMD5s))) + "-" + partCount, quoted.
//
// SeaweedFS computes this the same way (filer_multipart.go,
// fmt.Sprintf("%x-%d", md5.Sum(concat), n)), so a test can predict the
// expected ETag without ever asking the server.
func MultipartETag(partMD5s [][16]byte) string {
	var concat []byte
	for _, d := range partMD5s {
		concat = append(concat, d[:]...)
	}
	sum := md5.Sum(concat)
	return fmt.Sprintf(`"%x-%d"`, sum, len(partMD5s))
}
