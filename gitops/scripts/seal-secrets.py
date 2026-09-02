#!/usr/bin/env python3
"""Seal the ModelOps platform credentials for THIS cluster.

Sealed Secrets are encrypted with a specific cluster's controller key, so the
committed SealedSecrets only decrypt on the cluster they were generated for.
Run this script once per (re)deployed cluster, from a machine with `oc` logged
into that cluster and `kubeseal` on PATH, to (re)generate genuinely random
credential values, seal them, and write the SealedSecret files back into
gitops/components/. Commit the result.

Prerequisites:
  - The sealed-secrets controller is running (sealed-secrets Application, wave
    -1; oc wait --for=condition=Available deployment/sealed-secrets-controller -n kube-system).
  - kubeseal 0.39.x on PATH (https://github.com/bitnami-labs/sealed-secrets/releases).
  - htpasswd (apache2-utils) and openssl available for value generation.

Identity coupling (rotate together, never independently):
  - MinIO root user/password is ONE value shared by minio-credentials, the
    scan/result S3 credentials, results-ui s3-creds, and the intake UI prefill
    defaults -- all five must carry THE SAME user/password.
  - The zotadmin identity is bcrypt-hashed in zot-htpasswd and carried in
    plaintext in zot-push-credentials -- the two MUST stay in lockstep.
  - The MaaS DB password appears in both maas-postgres-credentials and the
    DB_CONNECTION_URL inside maas-db-config -- same rule.

Only the encrypted SealedSecret files are written to the repo. Credential
values are never written to disk or committed.
"""
import base64
import json
import os
import secrets
import string
import subprocess
import sys

import yaml

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

KUBESEAL_ARGS = ["--controller-namespace", "kube-system",
                 "--controller-name", "sealed-secrets-controller",
                 "--format", "yaml"]

S3_ENDPOINT = "http://minio.modelops-storage.svc.cluster.local:9000"


def rand_password(n=28):
    alphabet = string.ascii_letters + string.digits + "-_"
    return "".join(secrets.choice(alphabet) for _ in range(n))


def rand_minio_user():
    return "modelops-" + secrets.token_hex(8)


def bcrypt_htpasswd(user, password):
    proc = subprocess.run(["htpasswd", "-nbB", "-C", "10", user, password],
                          capture_output=True, text=True, check=True)
    return proc.stdout.strip()


def seal(name, namespace, data):
    secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {"name": name, "namespace": namespace},
        "type": "Opaque",
        "data": {k: base64.b64encode(v.encode()).decode() for k, v in data.items()},
    }
    proc = subprocess.run(["kubeseal"] + KUBESEAL_ARGS,
                          input=json.dumps(secret),
                          capture_output=True, text=True,
                          check=False)
    if proc.returncode != 0:
        raise RuntimeError("kubeseal failed for %s/%s: %s" % (namespace, name, proc.stderr))
    doc = yaml.safe_load(proc.stdout)
    doc.setdefault("spec", {}).setdefault("template", {}).setdefault("metadata", {})
    doc["spec"]["template"]["metadata"].setdefault("labels", {})
    doc["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/part-of"] = "modelops"
    return "---\n" + yaml.safe_dump(doc, default_flow_style=False, sort_keys=False)


def write(rel, namespace, name, data):
    dest = os.path.join(REPO, "gitops", "components", rel)
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    with open(dest, "w") as f:
        f.write(seal(name, namespace, data))
    print("wrote %s (sealed %s/%s, %d keys)" % (rel, namespace, name, len(data)))


def main():
    minio_user = rand_minio_user()
    minio_pass = rand_password()
    zot_pass = rand_password()
    zot_htpasswd = bcrypt_htpasswd("zotadmin", zot_pass)
    maas_pass = rand_password()
    mysql_pass = rand_password()
    mysql_root_pass = rand_password()

    write("minio/minio-credentials-sealedsecret.yaml", "modelops-storage",
          "minio-credentials", {"root-user": minio_user, "root-password": minio_pass})

    write("zot/zot-htpasswd-sealedsecret.yaml", "modelops-zot",
          "zot-htpasswd", {"htpasswd": zot_htpasswd})

    write("runtime-config/scan-s3-credentials-sealedsecret.yaml", "sandbox",
          "scan-s3-credentials", {"accessKeyId": minio_user, "endpoint": S3_ENDPOINT,
                                  "secretAccessKey": minio_pass})
    write("runtime-config/result-s3-credentials-sealedsecret.yaml", "sandbox",
          "result-s3-credentials", {"accessKeyId": minio_user, "endpoint": S3_ENDPOINT,
                                    "secretAccessKey": minio_pass})
    write("runtime-config/zot-push-credentials-sealedsecret.yaml", "sandbox",
          "zot-push-credentials", {"username": "zotadmin", "password": zot_pass})

    write("results-ui/s3-creds-sealedsecret.yaml", "sandbox",
          "s3-creds", {"S3_BUCKET_NAME": "benchmark-results", "S3_ACCESS_KEY": minio_user,
                       "S3_SECRET_KEY": minio_pass, "S3_ENDPOINT_URL": S3_ENDPOINT})

    write("model-intake-ui/model-intake-secrets-sealedsecret.yaml", "sandbox",
          "model-intake-secrets", {"DEFAULT_S3_ACCESS_KEY": minio_user,
                                   "DEFAULT_S3_SECRET_KEY": minio_pass})

    write("model-registry/mysql-sealedsecret.yaml", "rhoai-model-registries",
          "mysql", {"database-name": "sampledb", "database-password": mysql_pass,
                    "database-root-password": mysql_root_pass, "database-user": "admin"})

    write("maas/maas-db-config-sealedsecret.yaml", "redhat-ai-gateway-infra",
          "maas-db-config", {"DB_CONNECTION_URL": "postgresql://maas:%s@maas-postgres.maas-infra.svc.cluster.local:5432/maas" % maas_pass})

    write("maas/maas-postgres-credentials-sealedsecret.yaml", "maas-infra",
          "maas-postgres-credentials", {"database-user": "maas",
                                        "database-password": maas_pass, "database-name": "maas"})


if __name__ == "__main__":
    main()
