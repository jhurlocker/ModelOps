"""
S3 upload helper - used by multiple tasks to upload benchmark/evaluation results.

Environment variables:
  S3_ENDPOINT_URL, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY
  S3_BUCKET         target bucket
  TIMESTAMP          prefix for the key
  PATTERN            glob pattern for files to upload (default: *)
  PREFIX_OVERRIDE    optional override for the S3 key prefix
"""

import glob
import os
import sys
from datetime import datetime, timezone

import boto3
from botocore.client import Config


def safe_timestamp(env_key):
    val = os.environ.get(env_key, "")
    if not val or "$" in val:
        return datetime.now(timezone.utc).strftime("%Y%m%d_%H%M%S")
    return val


def safe_prefix(env_key):
    val = os.environ.get(env_key, "")
    if not val:
        return safe_timestamp("") + "_compliance_artifact"
    if "$" in val:
        return safe_timestamp("") + "_compliance_artifact"
    return val


def main():
    endpoint = os.environ.get("S3_ENDPOINT_URL")
    access_key = os.environ.get("S3_ACCESS_KEY_ID")
    secret_key = os.environ.get("S3_SECRET_ACCESS_KEY")
    bucket = os.environ.get("S3_BUCKET", "benchmark-results")
    timestamp = safe_timestamp("TIMESTAMP")
    pattern = os.environ.get("PATTERN", "*")
    prefix_override = safe_prefix("PREFIX_OVERRIDE")

    s3 = boto3.client(
        "s3", endpoint_url=endpoint,
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        config=Config(s3={"addressing_style": "path"}),
    )

    existing = [b["Name"] for b in s3.list_buckets().get("Buckets", [])]
    if bucket not in existing:
        s3.create_bucket(Bucket=bucket)
        print(f"Created bucket '{bucket}'.")

    pattern_parts = []
    if pattern == "*":
        pattern_parts = ["*"]
    else:
        pattern_parts = pattern.split(",")

    for p in pattern_parts:
        for path in glob.glob(p.strip()):
            if not os.path.isfile(path):
                continue
            prefix = prefix_override or timestamp
            key = f"{prefix}/{os.path.basename(path)}"
            try:
                s3.upload_file(path, bucket, key)
                print(f"Uploaded {path} -> s3://{bucket}/{key}")
            except Exception as e:
                print(f"WARN: failed to upload {path}: {e}")


if __name__ == "__main__":
    main()
