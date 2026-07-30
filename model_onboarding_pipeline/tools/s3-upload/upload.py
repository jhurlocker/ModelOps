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
from botocore.exceptions import ClientError


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


def ensure_bucket(s3, bucket):
    try:
        s3.head_bucket(Bucket=bucket)
        return True
    except ClientError as e:
        code = e.response["Error"]["Code"]
        if code == "404" or code == "NoSuchBucket":
            try:
                s3.create_bucket(Bucket=bucket)
                print(f"Created bucket '{bucket}'.")
                return True
            except ClientError as ce:
                print(f"ERROR: cannot create bucket '{bucket}': {ce}", file=sys.stderr)
                return False
        print(f"ERROR: cannot access bucket '{bucket}': {e}", file=sys.stderr)
        return False


def main():
    endpoint = os.environ.get("S3_ENDPOINT_URL")
    access_key = os.environ.get("S3_ACCESS_KEY_ID")
    secret_key = os.environ.get("S3_SECRET_ACCESS_KEY")
    bucket = os.environ.get("S3_BUCKET", "benchmark-results")
    timestamp = safe_timestamp("TIMESTAMP")
    pattern = os.environ.get("PATTERN", "*")
    prefix_override = safe_prefix("PREFIX_OVERRIDE")

    if not endpoint:
        print("ERROR: S3_ENDPOINT_URL is required", file=sys.stderr)
        sys.exit(1)
    if not access_key or not secret_key:
        print("ERROR: S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required", file=sys.stderr)
        sys.exit(1)

    s3 = boto3.client(
        "s3", endpoint_url=endpoint,
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        config=Config(s3={"addressing_style": "path"}),
    )

    if not ensure_bucket(s3, bucket):
        sys.exit(1)

    pattern_parts = ["*"] if pattern == "*" else [p.strip() for p in pattern.split(",")]
    uploaded = 0

    for pat in pattern_parts:
        for path in glob.glob(pat):
            if not os.path.isfile(path):
                continue
            prefix = prefix_override or timestamp
            key = f"{prefix}/{os.path.basename(path)}"
            try:
                s3.upload_file(path, bucket, key)
                print(f"Uploaded {path} -> s3://{bucket}/{key}")
                uploaded += 1
            except Exception as e:
                print(f"WARN: failed to upload {path}: {e}", file=sys.stderr)

    if uploaded == 0:
        print("WARN: no files matched the upload pattern", file=sys.stderr)


if __name__ == "__main__":
    main()
