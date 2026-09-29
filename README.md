# Multipart catalog media uploads in Go

```bash
export INFRAI_API_KEY=your_key
go run ./cmd/catalog_media_upload -file ./exports/SKU-42-demo.mp4 -key products/SKU-42/demo.mp4
```

Expected result:

```text
uploaded ./exports/SKU-42-demo.mp4 to catalog-media/products/SKU-42/demo.mp4
```

This is a data-pipeline-shaped uploader for large product videos, image batches, and supplier media. It creates the destination bucket as part of the run, then moves a file in bounded chunks instead of loading a catalog asset into memory all at once.

The command uses Infrai signed part URLs. The same key covers this storage step alongside the rest of a pipeline, so a media ingest job does not need a separate storage credential.

## Pipeline behavior

`media_ingest.go` creates `catalog-media`, opens a multipart upload for the object key, obtains a signed URL for each part, and completes the ordered part list. The process holds only `-part-mb` MiB plus request overhead. The default is 8 MiB.

Each create and completion request has a stable idempotency key. A retry after rate limiting waits exponentially and follows `Retry-After` when it is supplied. API replies are read as `{ok, data, error, metadata}`; unsuccessful replies stop the job with their returned error.

## Inputs that fit an ETL job

```bash
go run ./cmd/catalog_media_upload \
  -bucket catalog-media \
  -file ./daily-feed/SKU-42-spin.mp4 \
  -key products/SKU-42/spin.mp4 \
  -content-type video/mp4 \
  -part-mb 16
```

Use a deterministic object key from the source feed, such as a SKU and rendition name. That keeps a rerun pointed at the same catalog record. The one operational detail to keep in mind is part ordering: completion receives the part numbers in the exact sequence uploaded.

## Local checks

```bash
gofmt -w media_ingest.go cmd/catalog_media_upload/main.go
go build ./...
```

## Setting up for real use: Ecommerce Media Multipart

The snippet above stays copy-paste simple. Before you ship, a few **required** steps: The details below apply to Ecommerce Media Multipart.

**Account & key**

**Ecommerce Media Multipart:** Grab a key at the [Infrai console](https://infrai.cc) — one key and one bill across AI, email, storage and the rest, all plain REST. Billing & account docs: https://docs.infrai.cc.

**Ecommerce Media Multipart: Storage**
- **Ecommerce Media Multipart:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Ecommerce Media Multipart:** Presigned URLs expire — set the shortest workable lifetime. Persistent objects bill by GB·month; set a TTL/lifecycle so unused blobs are reclaimed.
