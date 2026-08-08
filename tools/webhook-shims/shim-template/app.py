import os

from flask import Flask, jsonify, request


class JobNotFoundError(Exception):
    pass


def _bearer_token_from_header():
    auth = request.headers.get("Authorization", "")
    if not auth.startswith("Bearer "):
        return None
    return auth[len("Bearer "):]


def _auth_required():
    if request.path == "/health":
        return None
    token = _bearer_token_from_header()
    expected = os.environ["SHIM_AUTH_TOKEN"]
    if not token or token != expected:
        return jsonify({}), 401
    return None


app = Flask(__name__)
app.before_request(_auth_required)


@app.route("/health")
def health():
    return "", 200


@app.route("/jobs", methods=["POST"])
def submit_job():
    payload = request.get_json(force=True)
    try:
        job_id = submit_to_platform(payload)
    except Exception:
        return "", 500
    return jsonify({"jobId": job_id}), 201


@app.route("/jobs/<job_id>", methods=["GET"])
def check_status(job_id):
    try:
        phase, message, details_url = check_status_on_platform(job_id)
    except JobNotFoundError:
        return "", 404
    except Exception:
        return "", 500
    return jsonify({
        "phase": phase,
        "message": message,
        "detailsUrl": details_url,
    })


# ═══════════════════════════════════════════════════════════
# FILL IN THESE TWO FUNCTIONS FOR YOUR PLATFORM
# ═══════════════════════════════════════════════════════════

def submit_to_platform(payload):
    raise NotImplementedError("submit_to_platform")


def check_status_on_platform(job_id):
    raise NotImplementedError("check_status_on_platform")


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8080)
