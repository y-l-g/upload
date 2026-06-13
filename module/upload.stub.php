<?php

/** @generate-class-entries */

function pogo_upload_create(array $intent, string $store = 'default'): array
{
}

function pogo_upload_progress(string $uploadId, string $store = 'default'): ?array
{
}

function pogo_upload_cancel(string $uploadId, string $store = 'default'): bool
{
}

function pogo_upload_status(?string $store = null): string
{
}
