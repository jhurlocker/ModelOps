# Approval Workflow Troubleshooting

## wait-for-approval Never Progresses

- Confirm `approval-api-url` is reachable from inside the cluster — use the in-cluster Service DNS (`http://model-intake.vllm.svc.cluster.local:8080`), not the public Route.
- If `POST /api/plans` failed, the TaskRun log shows `FATAL: could not submit plan`.
- A human must click Approve/Reject at `/plans/<plan_id>` in the app (or call the JSON API directly).
- If `approval-timeout-seconds` expires with no decision, the Task fails and halts the pipeline before `deploy-model`.

## Model Intake App Can't Create PipelineRuns

Check the `model-intake` ServiceAccount RBAC:
- Role `model-intake-tekton` in the `deployment.yaml` grants `create`/`get`/`list`/`watch` on `pipelineruns.tekton.dev`
- The Role also needs `get`/`list` on `pipelines.tekton.dev`

Check app logs:

```bash
oc logs -n vllm deployment/model-intake
```

## Plan Status Not Updating

- The `wait-for-approval` task polls `GET /api/plans/<plan_id>` for the `status` field.
- If the app's SQLite DB is corrupted or the PVC is full, writes may fail silently.
- Check app logs for database errors.

## Auto-Approval

Set `approval-api-url` to empty to skip the human gate entirely. Only for development/testing.
