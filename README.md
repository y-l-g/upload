# Pogo Upload

Pogo Upload is a FrankenPHP-native upload ingress module.

It lets PHP applications create signed upload intents, then lets a Caddy/Go
handler receive the upload body, enforce limits, stream bytes to storage, compute
checksums, and notify a PHP worker when the upload is complete or failed.

Pogo Upload is for large or controlled single-file uploads. It is not a file
manager, media processor, CDN, generic object-storage abstraction, virus scanner,
or replacement for normal framework upload handling for small forms.

## Production Model

- Scope: one FrankenPHP process for in-flight progress and worker dispatch.
- Data plane: Go HTTP handler streams request bodies to a configured backend.
- Control plane: PHP creates upload intents and handles completion/failure
  events.
- Authorization: upload URLs are signed, short-lived bearer tokens created by
  PHP code through the native API.
- Backends: local filesystem for v1. S3-compatible object storage can be added
  behind the same store interface without changing the PHP API.
- Durability: stored objects are durable according to the configured backend.
  In-flight progress is process-local and is lost on restart.
- Delivery: completion events are sent before the successful HTTP response.
  Failure events are best-effort. Event handlers must be idempotent.
- Scaling: multiple replicas may accept uploads when they share the same signing
  secret and storage backend. Progress and cancellation remain process-local.

Use Pogo Upload when the application should keep normal Laravel/Symfony
authorization and business rules, but PHP should not spend request time moving
large upload bodies.

Use standard Laravel/Symfony uploads when files are small, multipart form fields
must be processed together with the file, or the framework should receive an
`UploadedFile` object directly.

## Request Flow

1. A normal Laravel/Symfony controller authenticates the user and validates the
   requested upload.
2. The controller calls `pogo_upload_create()` with the destination key, size
   limit, accepted content types, and application metadata.
3. The client uploads the raw file body to the returned signed URL.
4. The Caddy handler validates the token, enforces limits, streams the body to
   storage, and computes a SHA-256 checksum.
5. The module sends a `completed` or `failed` event to the configured PHP worker.
6. The PHP worker records the result in the application database and returns a
   small JSON acknowledgement.

The upload endpoint accepts one file per request. Multiple files are represented
as multiple upload intents.

## Native API

The native API uses PHP arrays and binary-safe strings. Framework code can wrap
these functions in application-local services.

```php
function pogo_upload_create(array $intent, string $store = 'default'): array;
function pogo_upload_progress(string $uploadId, string $store = 'default'): ?array;
function pogo_upload_cancel(string $uploadId, string $store = 'default'): bool;
function pogo_upload_status(?string $store = null): string;
```

### Upload Intent

```php
[
    'key' => 'users/123/uploads/avatar.jpg',
    'filename' => 'avatar.jpg',
    'content_types' => ['image/jpeg', 'image/png'],
    'max_bytes' => 5242880,
    'overwrite' => false,
    'expires_in' => 900,
    'metadata' => [
        'user_id' => '123',
        'purpose' => 'avatar',
    ],
]
```

Intent fields:

- `key`: required backend object key. It must be relative, normalized, and must
  not contain `..`, a leading slash, or NUL bytes.
- `filename`: optional original filename for application metadata.
- `content_types`: optional accepted request `Content-Type` values. The value is
  enforced as a request constraint, not trusted as file proof.
- `max_bytes`: required positive maximum body size.
- `overwrite`: optional boolean. Defaults to `false`. When false, an upload fails
  with a conflict if the final key already exists.
- `expires_in`: optional token lifetime in seconds. Defaults to the store
  `token_ttl`.
- `metadata`: optional string map returned unchanged in worker events. It is for
  application context only and is not used by the storage backend.

Invalid intents throw `ValueError`. Missing stores, backend failures, signing
failures, and unavailable module state throw `RuntimeException`.

### Create Response

```php
[
    'upload_id' => 'upl_01jz2k4v4x9s8m1px6h0y7f2am',
    'method' => 'PUT',
    'url' => '/_pogo/upload/eyJhbGciOiJIUzI1NiJ9...',
    'headers' => [
        'content-type' => 'image/jpeg',
    ],
    'expires_at' => '2026-05-22T12:45:00Z',
    'max_bytes' => 5242880,
]
```

The returned URL is a bearer credential. Applications should send it only to the
authenticated client that is allowed to upload the object.

### Progress Response

`pogo_upload_progress()` returns process-local progress for active and recently
finished uploads:

```php
[
    'upload_id' => 'upl_01jz2k4v4x9s8m1px6h0y7f2am',
    'state' => 'receiving',
    'bytes_received' => 1048576,
    'max_bytes' => 5242880,
    'started_at' => '2026-05-22T12:30:08Z',
]
```

It returns `null` when the upload is unknown to the current process or its
progress record expired. Progress is an operational convenience, not durable
application state.

### Status Response

`pogo_upload_status()` returns JSON with readiness and per-store counters:

- configured limits
- active uploads
- accepted, completed, failed, and cancelled uploads
- rejected tokens, expired tokens, size-limit failures, and content-type failures
- bytes received
- backend write failures
- worker event failures

Expose the same counters as Prometheus metrics when Caddy metrics are enabled.
Do not expose object keys, token values, filenames, or metadata in metrics.

## Upload Endpoint

The upload endpoint is a Caddy HTTP handler. It receives signed URLs generated by
`pogo_upload_create()`.

Accepted request:

```http
PUT /_pogo/upload/{token}
Content-Type: image/jpeg
Content-Length: 428129

... raw file bytes ...
```

Successful response:

```json
{
  "ok": true,
  "upload_id": "upl_01jz2k4v4x9s8m1px6h0y7f2am",
  "key": "users/123/uploads/avatar.jpg",
  "bytes": 428129,
  "sha256": "1ecb9f..."
}
```

Failed response:

```json
{
  "ok": false,
  "upload_id": "upl_01jz2k4v4x9s8m1px6h0y7f2am",
  "error": {
    "code": "too_large",
    "message": "upload exceeded max_bytes"
  }
}
```

HTTP status codes should match the failure category: `400` for malformed
requests, `401` for invalid tokens, `413` for body limits, `415` for rejected
content types, `499`/`408` for interrupted clients when available, and `5xx` for
backend or worker failures.

## Caddy Configuration

```caddy
{
    frankenphp

    pogo_upload {
        store default {
            worker public/upload-worker.php
            signing_secret {$POGO_UPLOAD_SECRET}

            backend local {
                root storage/app/pogo-uploads
            }

            token_ttl 15m
            max_upload_bytes 1073741824
            max_concurrency 32
            read_timeout 30s
            complete_timeout 10s
            progress_ttl 10m
        }
    }
}

:80 {
    handle /_pogo/upload/* {
        pogo_upload default
    }

    php_server
}
```

Store directives:

- `worker`: PHP worker script that receives completion/failure events. Required.
- `signing_secret`: secret used to sign upload URLs. Required for stable tokens
  across reloads or multiple replicas.
- `backend`: storage backend. `local` is required for v1.
- `root`: local backend root directory. Object keys are resolved inside this
  directory.
- `token_ttl`: default signed URL lifetime. Default `15m`.
- `max_upload_bytes`: hard store-level upload limit. Default `1073741824`.
- `max_concurrency`: max in-flight uploads for the store. Default `32`.
- `read_timeout`: max idle time while reading the request body. Default `30s`.
- `complete_timeout`: max time to wait for the PHP completion worker. Default
  `10s`.
- `progress_ttl`: how long finished progress records remain visible. Default
  `10m`.

The HTTP handler directive receives the store name:

```caddy
handle /_pogo/upload/* {
    pogo_upload default
}
```

Multiple stores may be configured for different applications, tenants, storage
roots, or limits.

## Worker Events

The worker is a normal FrankenPHP worker script. It receives JSON event payloads
and returns JSON.

### Completed Event

```json
{
  "type": "completed",
  "upload_id": "upl_01jz2k4v4x9s8m1px6h0y7f2am",
  "store": "default",
  "key": "users/123/uploads/avatar.jpg",
  "filename": "avatar.jpg",
  "content_type": "image/jpeg",
  "bytes": 428129,
  "sha256": "1ecb9f...",
  "metadata": {
    "user_id": "123",
    "purpose": "avatar"
  },
  "started_at": "2026-05-22T12:30:08Z",
  "completed_at": "2026-05-22T12:30:09Z"
}
```

Expected response:

```json
{
  "ok": true
}
```

If the completed handler returns `ok: false` or times out, the module reports an
upload failure to the client and attempts to delete the stored object. The PHP
handler must still be idempotent because clients can retry uploads and Caddy can
reload while work is in progress.

### Failed Event

```json
{
  "type": "failed",
  "upload_id": "upl_01jz2k4v4x9s8m1px6h0y7f2am",
  "store": "default",
  "key": "users/123/uploads/avatar.jpg",
  "reason": "client_aborted",
  "bytes_received": 1048576,
  "metadata": {
    "user_id": "123",
    "purpose": "avatar"
  },
  "started_at": "2026-05-22T12:30:08Z",
  "failed_at": "2026-05-22T12:30:11Z"
}
```

Expected response:

```json
{
  "ok": true
}
```

Failure events are for cleanup and audit. A failed event handler error is logged
and counted but does not change the HTTP response if the upload already failed
for another reason.

## Framework Integration

This repository currently ships the native FrankenPHP module only. It does not
contain `pogo/laravel-upload`, `pogo/symfony-upload`, facades, bundles, DTOs, or
testing fakes.

Use normal Laravel, Symfony, or custom PHP controllers for authentication and
business rules, then call the native API directly or through an application-local
service:

```php
$intent = pogo_upload_create([
    'key' => 'users/'.$userId.'/avatars/'.bin2hex(random_bytes(16)).'.jpg',
    'filename' => 'avatar.jpg',
    'content_types' => ['image/jpeg', 'image/png'],
    'max_bytes' => 5242880,
    'metadata' => [
        'user_id' => (string) $userId,
        'purpose' => 'avatar',
    ],
]);
```

Handle completion and failure events in `public/upload-worker.php`:

```php
<?php

require_once dirname(__DIR__).'/vendor/autoload.php';

while (frankenphp_handle_request(static function (string $message): string {
    $event = json_decode($message, true, flags: JSON_THROW_ON_ERROR);

    // Resolve your application container here and persist the completed or
    // failed upload event in your own database.
    handle_upload_event($event);

    return json_encode(['ok' => true], JSON_THROW_ON_ERROR);
})) {
    gc_collect_cycles();
}
```

Framework adapters can be added later on top of this contract. They should not
replace Laravel's or Symfony's normal `UploadedFile` handling for small forms;
their job would be to wrap intent creation, expose typed worker events, and add
test helpers for application code.

## Architecture

Pogo Upload has four layers:

1. PHP extension functions expose a small intent/progress/status API.
2. A Caddy app owns store configuration, signing keys, backend clients, worker
   pools, and metrics.
3. A Caddy HTTP handler validates upload tokens and streams request bodies to
   the selected store backend.
4. Application code or future framework adapters wrap the native API and
   translate worker events into framework-friendly services and DTOs.

The Go upload path should avoid buffering whole files in memory. It reads the
request body in fixed-size chunks, updates progress and checksum state, writes to
the backend, and aborts as soon as a limit or context cancellation is reached.

## Semantics

- Upload URLs are single-use by default. Reusing a completed upload token returns
  a conflict response while its progress record is still known.
- Backend writes use a temporary object or file and commit to the final key only
  after the body is fully received and validated. If `overwrite` is false and the
  final key already exists, the temporary object is deleted and the request fails.
- Client disconnects cancel the backend write and emit a `failed` event when
  possible.
- `pogo_upload_cancel()` cancels only uploads known to the current process.
- The backend object key is chosen by PHP and signed into the token. Clients
  cannot choose or override it.
- Content length is validated when present. Streaming bodies without
  `Content-Length` are allowed but still limited while reading.
- Response bodies are small JSON documents. The module never returns uploaded
  file bytes.
- Caddy reload stops accepting new uploads for the old config and lets active
  uploads finish until the server shutdown context is cancelled.

## Security

- Treat returned upload URLs as bearer tokens.
- Keep token TTLs short.
- Use HTTPS in production.
- Do not trust filenames or content types for file safety.
- Store objects outside the public web root unless the application explicitly
  publishes them.
- Normalize and validate keys before signing.
- Do not include secrets, full tokens, or user-provided metadata in logs or
  metrics.

## Non-Goals

- Multipart form parsing in v1
- Multiple files in one request
- Image, video, or document processing
- Antivirus scanning
- Browser rendering or JavaScript execution
- Durable progress tracking
- Distributed upload coordination
- Replacing Laravel Filesystem, Symfony HttpFoundation, or Flysystem
