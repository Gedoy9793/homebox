"""Lightweight unit tests that do not require the DINOv2 model weights."""

from __future__ import annotations

import numpy as np
import pytest

from app.store import GroupStore, StoreManager


def test_group_store_upsert_search_delete(tmp_path):
    dim = 8
    store = GroupStore("g1", dim=dim, model_name="test-model", root=tmp_path)

    v1 = np.zeros(dim, dtype=np.float32)
    v1[0] = 1.0
    v2 = np.zeros(dim, dtype=np.float32)
    v2[1] = 1.0

    store.upsert("a1", "e1", v1)
    store.upsert("a2", "e2", v2)

    assert store.attachment_ids() == ["a1", "a2"]

    hits = store.search(v1, top_k=2)
    assert hits[0].attachment_id == "a1"
    assert hits[0].entity_id == "e1"
    assert hits[0].score == pytest.approx(1.0, abs=1e-5)

    # Upsert same attachment replaces vector/entity.
    store.upsert("a1", "e1b", v2)
    assert store.attachment_ids() == ["a2", "a1"]
    hits = store.search(v2, top_k=2)
    assert hits[0].attachment_id in {"a1", "a2"}

    assert store.delete("a2") is True
    assert store.attachment_ids() == ["a1"]
    assert store.delete("missing") is False

    # Reload from disk.
    reloaded = GroupStore("g1", dim=dim, model_name="test-model", root=tmp_path)
    assert reloaded.attachment_ids() == ["a1"]
    hits = reloaded.search(v2, top_k=1)
    assert hits[0].attachment_id == "a1"
    assert hits[0].entity_id == "e1b"


def test_model_mismatch_resets(tmp_path):
    dim = 4
    store = GroupStore("g2", dim=dim, model_name="model-a", root=tmp_path)
    v = np.ones(dim, dtype=np.float32)
    store.upsert("a1", "e1", v)

    other = GroupStore("g2", dim=dim, model_name="model-b", root=tmp_path)
    assert other.attachment_ids() == []


def test_invalid_group_id():
    with pytest.raises(ValueError):
        GroupStore("../escape", dim=4, model_name="m", root="/tmp")


def test_store_manager(tmp_path):
    mgr = StoreManager(dim=4, model_name="m", root=tmp_path)
    a = mgr.get("group-a")
    b = mgr.get("group-a")
    assert a is b
