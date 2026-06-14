from __future__ import annotations

import re
from typing import Any

from .oem_client import OEMClient
from .utils import (
    ensure_required_tags,
    find_property_value,
    listener_short_name,
    short_hostname,
    tag_target_name,
)


PDB_TYPES = {"oracle_pdb"}
RAC_TYPES = {"rac_database"}


def _swap_p_s(name: str) -> str:
    if "p" in name:
        return name.replace("p", "s")
    if "s" in name:
        return name.replace("s", "p")
    return name


def _guess_primary_and_standby(prefix: str) -> tuple[str, str]:
    if "p" in prefix and "s" not in prefix:
        return prefix, _swap_p_s(prefix)
    if "s" in prefix and "p" not in prefix:
        return _swap_p_s(prefix), prefix
    return prefix, _swap_p_s(prefix)


def _regex_full(pattern: str) -> re.Pattern:
    return re.compile(pattern, re.IGNORECASE)


def _find_targets(
    targets: list[dict[str, Any]],
    regex_list: list[re.Pattern],
    type_name: str,
    unique: bool,
) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    for rgx in regex_list:
        matches = [t for t in targets if t.get("typeName") == type_name and rgx.fullmatch(t.get("name", ""))]
        if matches:
            results.extend(matches)
            if unique:
                break
    return results


def _find_target_by_name_type(
    targets: list[dict[str, Any]], name: str, type_name: str
) -> dict[str, Any] | None:
    for t in targets:
        if t.get("typeName") == type_name and t.get("name", "").lower() == name.lower():
            return t
    return None


def _enrich_oracle_database(
    target: dict[str, Any],
    client: OEMClient,
    targets: list[dict[str, Any]],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    extra_targets: list[dict[str, Any]] = []
    properties: dict[str, Any] | None = None
    try:
        properties = client.get_target_properties(target["id"])
    except Exception:
        properties = None

    items = (properties or {}).get("items") or []
    dg_role = find_property_value(items, "DataGuardStatus")
    machine_name = find_property_value(items, "MachineName")

    if machine_name:
        machine_name = machine_name.replace("-vip", "")

    if dg_role:
        target["dg_role"] = dg_role

    if machine_name:
        target["machine_name"] = machine_name
        listener_name = f"LISTENER_{machine_name}"
        target["listener_name"] = listener_name

        host_target = _find_target_by_name_type(targets, machine_name, "host")
        if host_target:
            extra_targets.append(host_target)

        listener_target = _find_target_by_name_type(targets, listener_name, "oracle_listener")
        if not listener_target:
            short = short_hostname(machine_name)
            if short:
                listener_target = _find_target_by_name_type(
                    targets, f"LISTENER_{short}", "oracle_listener"
                )
        if listener_target:
            extra_targets.append(listener_target)

    return target, extra_targets


def _apply_tags(
    target: dict[str, Any],
    oracle_dbsys_name: str | None,
    rac_name: str | None,
) -> dict[str, Any]:
    type_name = target.get("typeName") or ""
    tags = target.get("tags") or {}
    tags = dict(tags)

    display_name = tag_target_name(target.get("name", ""), type_name)

    # Always include required tags + self tag
    tags["target_name"] = display_name
    tags["target_type"] = type_name
    if type_name:
        tags[type_name] = display_name

    if type_name in {"rac_database", "oracle_pdb", "oracle_database"} and oracle_dbsys_name:
        tags["oracle_dbsys"] = oracle_dbsys_name
    if type_name in {"oracle_pdb", "oracle_database"} and rac_name:
        tags["rac_database"] = rac_name

    if type_name == "oracle_database":
        if target.get("dg_role"):
            tags["dg_role"] = target["dg_role"]
        machine_name = target.get("machine_name")
        if machine_name:
            short_machine = short_hostname(machine_name)
            if short_machine:
                tags["machine_name"] = short_machine
        listener_name = target.get("listener_name")
        if listener_name:
            short_listener = listener_short_name(listener_name.replace("LISTENER_", ""))
            if short_listener:
                tags["listener_name"] = short_listener

    target["tags"] = tags
    return target


def auto_map_system(
    targets: list[dict[str, Any]],
    root_name: str,
    root_type: str,
    client: OEMClient,
) -> list[dict[str, Any]]:
    prefix = root_name.split("_")[0] if root_type in PDB_TYPES else root_name
    primary_rac, standby_rac = _guess_primary_and_standby(prefix)
    primary_upper = primary_rac.upper()

    found: list[dict[str, Any]] = []
    found_by_id: dict[str, dict[str, Any]] = {}

    def add_target(item: dict[str, Any]) -> None:
        if item["id"] in found_by_id:
            return
        # Keep all fields (including enriched metadata) instead of truncating to base keys.
        found_by_id[item["id"]] = dict(item)
        found.append(found_by_id[item["id"]])

    def add_many(items: list[dict[str, Any]]) -> None:
        for item in items:
            add_target(item)
    
    # oracle_dbsys
    oracle_dbsys_primary = _find_targets(
        targets,
        [_regex_full(rf"{re.escape(primary_rac)}_sys"),_regex_full(rf"{re.escape(primary_rac)}_1_sys")],
        "oracle_dbsys",
        unique=True,
    )
    oracle_dbsys_stby = _find_targets(
        targets,
        [_regex_full(rf"{re.escape(standby_rac)}_sys"),_regex_full(rf"{re.escape(standby_rac)}_1_sys")],
        "oracle_dbsys",
        unique=True,
    )
    add_many(oracle_dbsys_primary)
    add_many(oracle_dbsys_stby)

    # rac_database
    rac_primary = _find_targets(
        targets,
        [_regex_full(rf"{re.escape(primary_rac)}"), _regex_full(rf"{re.escape(primary_rac)}_1")],
        "rac_database",
        unique=True,
    )
    rac_stby = _find_targets(
        targets,
        [_regex_full(rf"{re.escape(standby_rac)}"), _regex_full(rf"{re.escape(standby_rac)}_1")],
        "rac_database",
        unique=True,
    )
    add_many(rac_primary)
    add_many(rac_stby)

    # oracle_pdb (optional)
    pdb_primary = _find_targets(
        targets,
        [_regex_full(rf"{re.escape(primary_rac)}_{re.escape(primary_upper)}.*")],
        "oracle_pdb",
        unique=False,
    )
    pdb_stby = _find_targets(
        targets,
        [_regex_full(rf"{re.escape(standby_rac)}_{re.escape(primary_upper)}.*")],
        "oracle_pdb",
        unique=False,
    )
    add_many(pdb_primary)
    add_many(pdb_stby)

    # oracle_database

    oracle_db_primary = _find_targets(
        targets,
        [_regex_full(rf"^{re.escape(primary_rac)}(?:_\d+)?_{re.escape(primary_rac)}\d*$")],
        "oracle_database",
        unique=False,
    )
    oracle_db_stby = _find_targets(
        targets,
        [_regex_full(rf"^{re.escape(standby_rac)}(?:_\d+)?_{re.escape(standby_rac)}\d*$")],
        "oracle_database",
        unique=False,
    )
    for item in oracle_db_primary + oracle_db_stby:
        enriched, extra = _enrich_oracle_database({"id": item["id"], "name": item["name"], "typeName": item["typeName"]}, client, targets)
        add_target(enriched)
        add_many(extra)

    oracle_dbsys_name = None
    if oracle_dbsys_primary:
        oracle_dbsys_name = oracle_dbsys_primary[0]["name"]
    elif oracle_dbsys_stby:
        oracle_dbsys_name = oracle_dbsys_stby[0]["name"]

    # Apply tags based on context
    for target in found:
        type_name = target.get("typeName")
        rac_name = None
        if type_name in {"oracle_pdb", "oracle_database"}:
            rac_name = target.get("name", "").split("_")[0]
        elif type_name == "rac_database":
            rac_name = target.get("name")

        _apply_tags(target, oracle_dbsys_name, rac_name)

        # Ensure required tags even if missing
        ensure_required_tags(target)

    return found


def prepare_targets(
    cached_targets: list[dict[str, Any]],
    selected: list[dict[str, Any]],
    client: OEMClient,
) -> list[dict[str, Any]]:
    prepared: list[dict[str, Any]] = []
    for item in selected:
        base = {
            "id": item.get("id"),
            "name": item.get("name"),
            "typeName": item.get("typeName"),
            "tags": dict(item.get("tags") or {}),
        }
        if base["typeName"] == "oracle_database":
            enriched, _ = _enrich_oracle_database(base, client, cached_targets)
            base = enriched
        _apply_tags(base, None, None)
        ensure_required_tags(base)
        prepared.append(base)
    return prepared
