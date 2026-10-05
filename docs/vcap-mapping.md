# Why these tests: VCAP's actual S3 usage

This harness is not a generic S3 compatibility suite - the priority order of
its tests follows what VCAP's backend (`VCAP-STAGING-BACKEND`) actually
does today, as of 2026-10. Facts below are cited to file:line in that repo.

| VCAP behavior | Source | Harness coverage |
|---|---|---|
| `boto3.client("s3", ..., config=Config(signature_version="s3v4", s3={"addressing_style":"path"}))`, region `us-east-1` | `app/services/minio_storage.py:26-34` | every target is path-style SigV4, region configurable, default `us-east-1` (`internal/target`) |
| `upload_fileobj`/`upload_file` (managed transfer; multipart above the SDK's default threshold) | `minio_storage.py:66-71,86-91` | `tests/multipart`, `vcapsim`'s workspace/artifact uploads |
| `download_file` to a local path | `minio_storage.py:95-101` | `tests/integrity` large-object GET tests, `vcapsim` |
| `delete_object`, single key only | `minio_storage.py:103-107` | `tests/basic.TestDeleteThenGet`, `vcapsim` cleanup delete |
| `ContentType` always set on PUT (zip/pdf/text, or a mimetypes guess) | `minio_storage.py:47-55`; `upload.py:111`; `compile.py:772,791` | `tests/metadata.TestContentTypeRoundtrip`; `vcapsim` sets `application/zip`/`application/pdf`/`text/plain` |
| **No presigned URLs, by explicit policy** ("they would bypass authz and leak bucket topology") | `artifact_service.py:13`; `artifact_download_service.py:7-8` | presign tests are lower priority (see `docs/test-matrix.md`); `vcapsim` never presigns |
| No user metadata, tags or checksums set by the app | whole file, `minio_storage.py` | `tests/metadata` covers the mechanism anyway, since a future change could add it |
| `list_objects_v2` / `list_multipart_uploads` paginators (superadmin inventory) | `minio_admin_service.py:62-97` | `tests/list.TestListPaginationCompleteness` |
| Key layout: workspace `<task_uuid>` flat; artifacts `<project_id>/<task_uuid>/{name.pdf,main.log}`; versioned artifacts `projects/<id>/review-<n>/v<ver>/<type>.<ext>` | `upload.py:100`; `compile.py:771,790`; `minio_storage.py:109-147` | `vcapsim` uses the first two layouts directly; `tests/basic.TestSpecialKeys` includes the third as a literal key |
| Object sizes: workspace ZIPs and compiled PDFs are app-uploaded files, not benchmarked, but typical coursework archives | [INFERENCE] | `vcapsim` generates 1-30 MiB ZIPs; `tests/integrity` covers 0B-100MiB independently of vcapsim |
| `STORAGE_BACKEND`, `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_REGION`, `MINIO_USE_SSL`, `BUCKET_WORKSPACE`, `BUCKET_ARTIFACTS` | `app/config.py:39-50` | `vcapsim`'s `-workspace-bucket`/`-artifacts-bucket` flags and `SWFSIT_*` env mirror this naming so the mapping stays obvious |

## What this means for prioritization

1. **Multipart, overwrite, delete-then-write, and checksum-mode tests are
   P0** - they exercise exactly VCAP's upload/download/delete path.
2. **Presigned URL tests are P2** - VCAP does not use them. They exist in
   the design (not yet implemented; see `docs/test-matrix.md`) in case VCAP
   adopts them later, but a SeaweedFS presign bug would not affect VCAP
   today.
3. **vcapsim intentionally does not implement VCAP's HTTP API, auth, or
   database.** It is a workload shape, not a VCAP clone: same buckets, same
   key layout, same content types, same operation sequence, same rough
   object sizes. See the package doc comment in `cmd/vcapsim/main.go`.
4. **The backend's current pin is `boto3==1.35.99`**
   (`VCAP-STAGING-BACKEND/requirements.txt:16`), which predates
   botocore 1.36's default CRC32 flexible checksums. An SDK upgrade would
   change the checksum mode VCAP sends on the wire -
   `tests/integrity.TestChecksumModesProduceIdenticalBytes` exists
   specifically to catch any SeaweedFS-side difference in behavior between
   the two modes before VCAP upgrades.
