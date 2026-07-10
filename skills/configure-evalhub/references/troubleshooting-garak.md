# Garak EvalHub Troubleshooting

## Garak CLI Version Changes

v0.3.x vs v0.15.x have incompatible flags:

| Old Flag | New Flag |
|----------|----------|
| `--model` | `--target_type` or `-t` |
| `--model_args` | `--generator_options` |
| `--output_json_path` | `--report_prefix` |

Always use `--skip_unknown` to gracefully skip probes that don't exist in the installed version.

## Probes That Don't Exist in Garak 0.15

Removed probes: `availability`, `off_topic_safety_cases`, `confusion.leaky_completion`, `give_gdpr.gdpr_email`, `give_gdpr.gdpr_name`, `prompt_attack.scenario`.

Working probes for 0.15: `apikey.GetKey`, `atkgen.Tox`, `dan.AutoDANCached`, `dan.DanInTheWild`, `encoding.InjectBase64`, `leakreplay.GuardianCloze`.

## False-Passes

The task previously used `|| true` which swallowed garak failures. The updated task exits with an error if garak fails AND produces no JSON report. Check TaskRun logs for warnings.

## EvalHub Tenant Namespace

Without `evalhub.trustyai.opendatahub.io/tenant=` label, evaluation jobs stay `pending` — EvalHub can't create Jobs/ConfigMaps in the target namespace. Label and wait ~15s for operator-provisioned RBAC.
