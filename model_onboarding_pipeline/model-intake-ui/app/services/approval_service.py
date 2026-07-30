import json
import sqlite3
from datetime import datetime, timezone

from app.config import DB_PATH


def _get_db():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn


def init_db():
    conn = _get_db()
    conn.execute("""
        CREATE TABLE IF NOT EXISTS plans (
            plan_id TEXT PRIMARY KEY,
            pipelinerun_name TEXT,
            model_id TEXT,
            model_name TEXT,
            target_namespace TEXT,
            requested_by TEXT,
            plan_status TEXT,
            recommendation_md TEXT,
            deployment_options TEXT,
            gpu_inventory TEXT,
            recommended_vllm_command TEXT,
            status TEXT DEFAULT 'pending',
            decided_by TEXT,
            decision_comment TEXT,
            created_at TEXT,
            updated_at TEXT
        )
    """)
    conn.commit()
    conn.close()


def list_plans():
    conn = _get_db()
    rows = conn.execute("SELECT * FROM plans ORDER BY updated_at DESC").fetchall()
    conn.close()
    return [dict(r) for r in rows]


def get_plan(plan_id):
    conn = _get_db()
    row = conn.execute("SELECT * FROM plans WHERE plan_id = ?", (plan_id,)).fetchone()
    conn.close()
    return dict(row) if row else None


def upsert_plan(data):
    conn = _get_db()
    existing = conn.execute("SELECT plan_id FROM plans WHERE plan_id = ?", (data["plan_id"],)).fetchone()
    now = datetime.now(timezone.utc).isoformat()
    if existing:
        conn.execute("""
            UPDATE plans SET
                pipelinerun_name=?, model_id=?, model_name=?, target_namespace=?,
                requested_by=?, plan_status=?, recommendation_md=?, deployment_options=?,
                gpu_inventory=?, recommended_vllm_command=?, updated_at=?
            WHERE plan_id=?
        """, (
            data.get("pipelinerun_name", ""),
            data.get("model_id", ""),
            data.get("model_name", ""),
            data.get("target_namespace", ""),
            data.get("requested_by", ""),
            data.get("plan_status", ""),
            data.get("recommendation_md", ""),
            data.get("deployment_options", ""),
            data.get("gpu_inventory", ""),
            data.get("recommended_vllm_command", ""),
            now,
            data["plan_id"],
        ))
    else:
        conn.execute("""
            INSERT INTO plans (plan_id, pipelinerun_name, model_id, model_name, target_namespace,
                               requested_by, plan_status, recommendation_md, deployment_options,
                               gpu_inventory, recommended_vllm_command, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, (
            data["plan_id"],
            data.get("pipelinerun_name", ""),
            data.get("model_id", ""),
            data.get("model_name", ""),
            data.get("target_namespace", ""),
            data.get("requested_by", ""),
            data.get("plan_status", ""),
            data.get("recommendation_md", ""),
            data.get("deployment_options", ""),
            data.get("gpu_inventory", ""),
            data.get("recommended_vllm_command", ""),
            now,
            now,
        ))
    conn.commit()
    conn.close()


def decide_plan(plan_id, decision, decided_by, comment=""):
    now = datetime.now(timezone.utc).isoformat()
    conn = _get_db()
    conn.execute(
        "UPDATE plans SET status=?, decided_by=?, decision_comment=?, updated_at=? WHERE plan_id=?",
        (decision, decided_by, comment, now, plan_id),
    )
    conn.commit()
    conn.close()


def get_plan_by_pipelinerun(pipelinerun_name):
    conn = _get_db()
    row = conn.execute("SELECT * FROM plans WHERE pipelinerun_name = ?", (pipelinerun_name,)).fetchone()
    conn.close()
    return dict(row) if row else None
