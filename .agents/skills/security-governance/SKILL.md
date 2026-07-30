---
name: security-governance
description: Apply secure credentials, policy, approvals, evidence, provenance, lineage, and AI BOM practices.
---

# Security and Governance

Never store secret values in custom resources. Never copy raw secret values into PipelineRun parameters when runtime Secret injection is possible.

Use Secret references, `secretKeyRef`, projected volumes, workload identity, or external secret providers.

Do not introduce hard-coded fallback credentials. Do not log credentials or signed URLs.

Separate policy from request intent. Policy may define required scans, approved licenses, CVE thresholds, provenance, behavioral evaluations, approvals, isolation, retention, and promotion constraints.

Store large reports in an artifact store and retain durable references.

Capture source model and revision, license, checksums, image digests, SBOM, vulnerabilities, provenance, datasets, metrics, capacity, approvals, deployments, dependencies, and retirement history.

## Checklist

- [ ] no plaintext credentials in CRs
- [ ] no secret values in PipelineRun params
- [ ] no credential logging
- [ ] policy is reusable
- [ ] evidence is durable
- [ ] exact tool and image versions are captured
- [ ] approvals are attributable
- [ ] rejected paths retain evidence
