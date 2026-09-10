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

## Empty Results From `quick`

EvalHub's `quick` profile is a single `dan.Dan_11_0` smoke probe ([TrustyAI Garak `SCAN_PROFILES`](https://github.com/trustyai-explainability/llama-stack-provider-trustyai-garak)). Current Garak builds often skip that probe, so the job completes with `attack_success_rate` unset and the results viewer looks empty.

Use the taxonomy profiles from EvalHub `config/providers/garak.yaml` instead:

| Profile | What it scans |
|---------|----------------|
| `quality` | Toxicity, violence, hate speech (`probe_tags=quality`) |
| `avid_security` | AVID security taxonomy |
| `cwe` | CWE software-weakness probes |
| `avid_ethics` | Bias and harmful-content probes |
| `owasp_llm_top10` | Full OWASP LLM Top 10 (hours) |
| `avid` | Full AVID taxonomy (hours) |
| `intents` | KFP-only; skipped by the pipeline's simple-mode job |

The `security-scan` task defaults to `quality,avid_security,cwe` with `generations: 1` so each profile still writes per-probe ASR without an overnight run. Override with pipeline param `garak-benchmarks`.

## False-Passes

The task previously used `|| true` which swallowed garak failures. The updated task exits with an error if garak fails AND produces no JSON report. Check TaskRun logs for warnings.

## EvalHub Tenant Namespace

Without `evalhub.trustyai.opendatahub.io/tenant=` label, evaluation jobs stay `pending` — EvalHub can't create Jobs/ConfigMaps in the target namespace. Label and wait ~15s for operator-provisioned RBAC.
