import json  # Back to json!
import random

# --- Define Prompt Templates (No change here) ---

# 40% of prompts will be short
short_prompts = [
    "list pods",
    "check db status",
    "hello",
    "what's the weather in denver?",
    "delete user 4091",
    "show routes",
    "get logs for pod-abc-123",
    "who is online?",
    "reboot server-main-db",
    "what is 2+2?",
]

# 35% of prompts will be medium
medium_prompts = [
    "Explain the difference between a Kubernetes Deployment and a StatefulSet.",
    "How do I configure a Tekton EventListener for a GitHub webhook?",
    "Write a Python function to parse a JSON file and return a list of all top-level keys.",
    "What are the pros and cons of using vLLM vs. TGI for inference serving?",
    "Summarize the following text: [placeholder for a 3-paragraph article]",
    "My OpenShift build is failing. What does the 'ImagePullBackOff' error mean?",
    "Translate this to German: 'My CI/CD pipeline is broken and I need to fix it before the demo.'",
]

# 15% of prompts will be long (e.g., code/YAML)
long_yaml_example = """
Please analyze this Tekton Task YAML for any syntax errors or best-practice violations:
apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: deploy-using-helm
spec:
  params:
    - name: helm-chart-path
      description: The path to the helm chart
      type: string
    - name: release-name
      description: The name of the release
      type: string
  steps:
    - name: helm-deploy
      image: "alpine/helm:3.10.0"
      script: |
        #!/usr/bin/env sh
        echo "Deploying $(params.release-name) from $(params.helm-chart-path)..."
"""

# 10% of prompts will be HUGE (stack traces, log dumps)
huge_stack_trace_example = """
My application is crashing in production. Analyze this full stack trace and tell me the root cause.
`
Traceback (most recent call last):
  File "/opt/app-root/src/main.py", line 215, in <module>
    main()
  File "/opt/app-root/src/main.py", line 198, in main
    db.connect(os.environ.get('DATABASE_URL'))
  File "/opt/app-root/lib/python3.9/site-packages/db_client/connector.py", line 89, in _try_connect
    raise ConnectionTimeoutError(f"Failed to connect to {host} after {retries} attempts")
db_client.errors.ConnectionTimeoutError: Failed to connect to db.production.svc.cluster.local after 3 attempts
... (stack trace continues)
...
Last error received from driver:
psycopg2.OperationalError: could not connect to server: Connection refused
`
"""

# --- Generate the File ---

output_filename = "historical_prompts.jsonl"
num_prompts = 100

with open(output_filename, "w", encoding="utf-8") as f:
    for i in range(num_prompts):
        rand_val = random.random()

        if rand_val < 0.40:
            prompt_text = random.choice(short_prompts)
        elif rand_val < 0.75:
            prompt_text = random.choice(medium_prompts)
        elif rand_val < 0.90:
            prompt_text = long_yaml_example.replace(
                "deploy-using-helm", f"deploy-using-helm-{i:03d}"
            )
        else:
            extra_spam_lines = random.randint(5, 50)
            extra_spam = "\n... (simulated repeating log noise)..." * extra_spam_lines
            prompt_text = huge_stack_trace_example + extra_spam

        # --- CHANGE: Create the simple dictionary ---
        record = {
            "prompt": prompt_text.strip()
        }
        
        # --- CHANGE: Write the JSON object as a single line ---
        f.write(json.dumps(record) + "\n")

print(f"✅ Success! Generated {num_prompts} prompts in '{output_filename}'.")