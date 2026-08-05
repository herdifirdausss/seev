#!/usr/bin/env python3
"""Imports the versioned dashboard manifests under analytics/metabase/dashboards/
into a running Metabase instance via its REST API.

The YAML manifests are the source of truth (analytics/metabase/setup/README.md);
this script is the mechanical step that turns them into native-SQL Metabase
questions and dashboards, since Metabase has no YAML import format of its own.

Usage:
    ANALYTICS_METABASE_ADMIN_PASSWORD=... python3 import_dashboards.py
"""
import datetime as dt
import glob
import os
import re
import sys

import requests
import yaml

METABASE_URL = os.environ.get("ANALYTICS_METABASE_URL", "http://127.0.0.1:3000")
ADMIN_EMAIL = os.environ.get("ANALYTICS_METABASE_ADMIN_EMAIL", "c2-admin@example.com")
ADMIN_PASSWORD = os.environ["ANALYTICS_METABASE_ADMIN_PASSWORD"]
DASHBOARDS_DIR = os.path.join(os.path.dirname(__file__), "..", "dashboards")

VARIABLE_RE = re.compile(r"\{\{\s*(\w+)\s*\}\}")


def session():
    resp = requests.post(
        f"{METABASE_URL}/api/session",
        json={"username": ADMIN_EMAIL, "password": ADMIN_PASSWORD},
        timeout=30,
    )
    resp.raise_for_status()
    token = resp.json()["id"]
    s = requests.Session()
    s.headers["X-Metabase-Session"] = token
    return s


def get_or_create_collection(s, name, parent_id=None):
    existing = s.get(f"{METABASE_URL}/api/collection", timeout=30).json()
    for c in existing:
        if c.get("name") == name and c.get("archived") is False:
            return c["id"]
    resp = s.post(
        f"{METABASE_URL}/api/collection",
        json={"name": name, "parent_id": parent_id, "color": "#509EE3"},
        timeout=30,
    )
    resp.raise_for_status()
    return resp.json()["id"]


def native_parameters(sql):
    # {{start_date}}/{{end_date}} are two independent single-date variables
    # (used as "between {{start_date}} and {{end_date}}"), not a single
    # combined date/range variable — each needs its own default or Metabase
    # refuses to run the query at all ("missing required parameters").
    names = sorted(set(VARIABLE_RE.findall(sql)))
    today = dt.date.today()
    params = []
    for name in names:
        is_date = "date" in name
        default = None
        if name == "start_date":
            default = (today - dt.timedelta(days=90)).isoformat()
        elif name == "end_date":
            default = today.isoformat()
        params.append(
            {
                "id": name,
                "type": "date/single" if is_date else "category",
                "target": ["variable", ["template-tag", name]],
                "name": name,
                "slug": name,
                "default": default,
            }
        )
    return params, names


def template_tags(names):
    today = dt.date.today()
    tags = {}
    for name in names:
        default = None
        if name == "start_date":
            default = (today - dt.timedelta(days=90)).isoformat()
        elif name == "end_date":
            default = today.isoformat()
        tags[name] = {
            "id": name,
            "name": name,
            "display-name": name,
            "type": "date" if "date" in name else "text",
            "default": default,
        }
    return tags


def create_card(s, database_id, collection_id, name, sql):
    params, names = native_parameters(sql)
    resp = s.post(
        f"{METABASE_URL}/api/card",
        json={
            "name": name,
            "collection_id": collection_id,
            "dataset_query": {
                "type": "native",
                "native": {"query": sql, "template-tags": template_tags(names)},
                "database": database_id,
            },
            "display": "table",
            "visualization_settings": {},
            "parameters": params,
        },
        timeout=30,
    )
    resp.raise_for_status()
    return resp.json()["id"], params


def create_dashboard(s, name, collection_id, description):
    resp = s.post(
        f"{METABASE_URL}/api/dashboard",
        json={"name": name, "collection_id": collection_id, "description": description},
        timeout=30,
    )
    resp.raise_for_status()
    return resp.json()["id"]


def add_card_to_dashboard(s, dashboard_id, card_id, row, col, params):
    dashboard = s.get(f"{METABASE_URL}/api/dashboard/{dashboard_id}", timeout=30).json()
    cards = dashboard.get("dashcards", [])
    parameter_mappings = [
        {
            "parameter_id": p["id"],
            "card_id": card_id,
            "target": ["variable", ["template-tag", p["id"]]],
        }
        for p in params
    ]
    cards.append(
        {
            "id": -(len(cards) + 1),
            "card_id": card_id,
            "row": row,
            "col": col,
            "size_x": 6,
            "size_y": 4,
            "parameter_mappings": parameter_mappings,
        }
    )
    resp = s.put(
        f"{METABASE_URL}/api/dashboard/{dashboard_id}",
        json={"dashcards": cards},
        timeout=30,
    )
    resp.raise_for_status()


def main():
    s = session()
    databases = s.get(f"{METABASE_URL}/api/database", timeout=30).json()
    database_id = None
    for db in databases.get("data", databases if isinstance(databases, list) else []):
        if db.get("engine") == "clickhouse":
            database_id = db["id"]
            break
    if database_id is None:
        print("no clickhouse database configured in Metabase yet", file=sys.stderr)
        sys.exit(1)

    business = get_or_create_collection(s, "C2 / Business")
    operations = get_or_create_collection(s, "C2 / Operations")

    for path in sorted(glob.glob(os.path.join(DASHBOARDS_DIR, "*.yaml"))):
        with open(path, encoding="utf-8") as f:
            spec = yaml.safe_load(f)
        collection_id = operations if spec.get("collection", "").endswith("Operations") else business
        dashboard_id = create_dashboard(s, spec["name"], collection_id, spec.get("warning", ""))
        row = 0
        for card in spec.get("cards", []):
            if card["source"] == "connector REST evidence":
                # Not a ClickHouse-queryable card; skip the operational
                # connector-status card (covered by ./analytics/connect/scripts/status-connectors.sh).
                continue
            card_id, params = create_card(s, database_id, collection_id, card["name"], card["sql"])
            add_card_to_dashboard(s, dashboard_id, card_id, row, 0, params)
            row += 4
        print(f"imported {spec['name']} ({path})")


if __name__ == "__main__":
    main()
