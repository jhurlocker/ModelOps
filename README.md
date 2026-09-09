# ModelOps

End-to-end LLM onboarding on OpenShift (sandbox scan → approval → staging → registry → optional MaaS).

```bash
# Logged in with oc, cluster-admin recommended:
./deploy-all.sh
./deploy-all.sh --skip-maas          # omit Models-as-a-Service
./deploy-all.sh --skip-maas --skip-build
```