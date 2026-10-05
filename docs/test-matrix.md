# Test matrix

This lists every implemented test, grouped by package under `tests/`. IDs
match the original design matrix; not every ID from the design doc has an
implementation yet (see "Not yet implemented" at the bottom) - this file
reflects what actually runs, not the aspiration.

Every test verifies content (bytes/digests), headers, or explicit state
invariants - never just an HTTP status code.

**Status as of 2026-10-05:** `go test -short ./tests/...` passes in full
against `chrislusf/seaweedfs:4.48` via the compose stack, with one
exception noted below as an open question rather than silently skipped.

## tests/basic (`go test ./tests/basic`)

| Test | Verifies |
|---|---|
| `TestPutGetRoundtrip` | GET returns exactly the uploaded bytes (SHA-256) and the correct ETag |
| `TestHeadMatchesGet` | HEAD's Content-Length/ETag/Content-Type match GET's; Last-Modified present |
| `TestMissingKey` | GET/HEAD on a missing key: `NoSuchKey`, HTTP 404 |
| `TestMissingBucket` | GET against a bucket that was never created: `NoSuchBucket` |
| `TestDeleteThenGet` | DELETE then GET: `NoSuchKey` |
| `TestDeleteMissingKey` | DELETE on a key that never existed: success (204) |
| `TestOverwriteSequence` | large→small→large overwrites leave exactly the latest bytes, no stale tail |
| `TestEmptyObject` | 0-byte object: well-known empty-MD5 ETag, 0-length GET |
| `TestSpecialKeys` | space/unicode/reserved-character/nested keys round-trip and list verbatim |
| `TestDeleteObjectsBatch` | batch delete of 50 keys + 1 missing key: all reported `Deleted`, zero `Errors` |

## tests/integrity (`go test ./tests/integrity`)

| Test | Verifies |
|---|---|
| `TestPutGetAcrossSizes` | 0B .. 100MiB × {random,zero,cycle}: full-content SHA-256 match |
| `TestRangeAcrossChunkBoundaries` | ranged GET ±1 byte around the 4MiB/8MiB chunk boundaries: exact bytes |
| `TestContentMD5Correct` | PUT with a correct `Content-MD5`: succeeds |
| `TestContentMD5WrongRejectedAndPreviousIntact` | PUT with a wrong `Content-MD5`: rejected, **previous object version unchanged** |
| `TestLargeObjectRoundtrip` | 100 MiB object: full-content digest match |
| `TestChecksumModesProduceIdenticalBytes` | same object under both SDK checksum modes (`WhenSupported`/`WhenRequired`, i.e. the modern SDK default vs. boto3 1.35.99's behavior): identical server-visible content |

## tests/multipart (`go test ./tests/multipart`)

| Test | Verifies |
|---|---|
| `TestCompleteBasic` | 3-part upload; bytes = concatenation; ETag = AWS's `md5(concat(partMD5s))-N` format |
| `TestRetryPartOverwritesPrevious` | re-uploading a part before Complete: the retry's bytes win |
| `TestCompleteWithStaleETagFails` | completing with a part ETag made stale by a re-upload: `InvalidPart` **(currently `t.Skip`s - see "Known open questions" below)** |
| `TestCompleteOutOfOrderFails` | completing with parts out of ascending order: `InvalidPartOrder` |
| `TestEntityTooSmallOnNonFinalPart` | a non-final part < 5 MiB: `EntityTooSmall` |
| `TestAbortCleansUpEverything` | after Abort: `ListParts`/`UploadPart` → `NoSuchUpload`, key → `NoSuchKey` |
| `TestAbortAfterCompleteLeavesObjectIntact` | aborting an upload ID that was already completed does not delete the object (seaweedfs#11382) |
| `TestIncompleteUploadIsListedNotVisible` | an incomplete upload: key invisible, upload present in `ListMultipartUploads` |
| `TestConcurrentPartUploads` | 4 parts uploaded concurrently, completed: correct concatenation |
| `TestMultipartOverwritesSinglePartAndBack` | multipart-over-plain and plain-over-multipart overwrites: correct bytes and ETag format each time |
| `TestDeleteThenCompleteIsVisible` | DELETE then CompleteMultipartUpload on the same key: object is visible (seaweedfs#10583) |
| `TestConcurrentCompletesNeverInterleave` | two multipart uploads racing to complete the same key: final object matches exactly one of them |

## tests/metadata (`go test ./tests/metadata`)

| Test | Verifies |
|---|---|
| `TestContentTypeRoundtrip` | Content-Type with a parameter round-trips exactly |
| `TestUserMetadataRoundtrip` | `x-amz-meta-*` with UTF-8 values round-trips on both GET and HEAD |
| `TestOverwriteReplacesMetadata` | overwrite without a given metadata key removes it (no leftover keys) |
| `TestETagConsistentAcrossOperations` | ETag identical across PUT/HEAD/GET/List for one object version |
| `TestRangeEdgeCases` | first-byte, suffix, oversized-suffix (clamped), start-past-end (`InvalidRange`) |
| `TestConditionalGetIfNoneMatch` | matching `If-None-Match` → 304; non-matching → 200 |
| `TestConditionalOnMissingKeyIs404NotPrecondition` | conditional HEAD on a missing key: 404, not 412 (seaweedfs#10984) |
| `TestConditionalPutIfNoneMatchStar` | `If-None-Match: *` against an existing key: 412 |
| `TestLastModifiedMovesForward` | Last-Modified strictly increases across an overwrite |

## tests/auth (`go test ./tests/auth`)

| Test | Verifies | Requires |
|---|---|---|
| `TestAnonymousRejected` | unsigned request: 403 | - |
| `TestWrongSignature` | wrong secret key: `SignatureDoesNotMatch` | - |
| `TestReadOnlyIdentityCannotWrite` | read-only identity: GET/LIST ok, PUT/DELETE → 403 | `SWFSIT_RO_*` |
| `TestScopedIdentityCannotTouchOtherBucket` | bucket-scoped identity rejected outside its bucket | `SWFSIT_SCOPED_*` |

## tests/list (`go test ./tests/list`)

| Test | Verifies |
|---|---|
| `TestListPaginationCompleteness` | 130 keys, MaxKeys ∈ {1,7,1000}: every key seen exactly once |
| `TestDelimiterGrouping` | `/` delimiter groups into `CommonPrefixes`, excludes them from `Contents` |
| `TestReadAfterWriteConsistency` | a listing issued immediately after PUT/DELETE reflects it |

## tests/concurrency (`go test ./tests/concurrency`)

| Test | Invariant |
|---|---|
| `TestConcurrentPutsDistinctKeys` | 32 concurrent PUTs to distinct keys: each verifies independently |
| `TestConcurrentGetsSameObject` | 16 concurrent GETs of one object: all return identical, correct bytes |
| `TestPutDeleteRace` | concurrent PUT/DELETE on one key: final state is either absent or exactly the PUT's bytes, never corrupted |
| `TestConcurrentConditionalCreateExactlyOneWins` | 16 concurrent `If-None-Match: *` creates: exactly 1 success, rest 412 (seaweedfs#8800) |

## tests/errors (`go test ./tests/errors`)

| Test | Verifies |
|---|---|
| `TestErrorCodesAndStatus` | table of operations against bogus buckets/uploads: correct `Code` + HTTP status |
| `TestDeleteNonEmptyBucketRecursivelyDeletesByDefault` | `weed s3`'s `-allowDeleteBucketNotEmpty=true` default: `DeleteBucket` on a non-empty bucket succeeds and recursively deletes contents - a deliberate AWS deviation, see `docs/vcap-mapping.md` |


## Known open questions

Not-yet-resolved findings from real runs, kept visible (as a skipped test
with a clear message) rather than silently deleted or re-pinned to the
observed behavior:

- **`TestCompleteWithStaleETagFails`** (`tests/multipart`): completing a
  multipart upload with a part's stale ETag (one that was superseded by a
  re-upload of the same part number) succeeds against SeaweedFS 4.48,
  instead of the `InvalidPart` AWS returns. Not yet root-caused: it is
  unknown whether the completed object ends up with the old part's bytes,
  the new part's bytes, or something else. Needs: a byte-level repro, a
  check against a reference S3 implementation, and a search of
  `seaweedfs/seaweedfs` issues before concluding this is a bug.

## Not yet implemented

These were in the original design (see git history / issue tracker) but are
not in this repository yet - tracked as follow-up work, not claimed as done:

- Presigned URL tests (`tests/presign`) - lower priority because VCAP's
  current backend does not use presigned URLs (see `docs/vcap-mapping.md`).
- Chaos/reliability tests that require a Kubernetes pod-kill disruptor
  (`internal/disrupt`, `tests/reliability`).
- The Versity Gateway and AWS S3 reference-target wiring and `swfsit
  compare` classification tool.
- Raw aws-chunked trailer fault injection (`internal/rawhttp`) for
  regression-testing seaweedfs#11582-class bugs.
- `docs/diagnostics.md` bundle automation (`swfsit diag`).

Each is a self-contained extension: the `target.Target`/`s3x` abstraction
and `testkit` helpers already support adding them without changing existing
tests.
