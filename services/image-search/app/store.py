"""Per-group FAISS index persistence."""

from __future__ import annotations

import json
import logging
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import faiss
import numpy as np

from . import config

logger = logging.getLogger(__name__)


@dataclass
class SearchHit:
    attachment_id: str
    entity_id: str
    score: float


class GroupStore:
    """FAISS IndexFlatIP + meta.json for one group_id."""

    def __init__(self, group_id: str, dim: int, model_name: str, root: Path = config.DATA_DIR) -> None:
        if not group_id or "/" in group_id or "\\" in group_id or group_id in (".", ".."):
            raise ValueError("invalid group_id")
        self.group_id = group_id
        self.dim = dim
        self.model_name = model_name
        self.dir = root / group_id
        self.index_path = self.dir / "index.faiss"
        self.meta_path = self.dir / "meta.json"
        self._lock = threading.RLock()
        # Parallel to FAISS row order.
        self.entries: list[dict[str, str]] = []
        self.index = faiss.IndexFlatIP(dim)
        self._load()

    def _load(self) -> None:
        self.dir.mkdir(parents=True, exist_ok=True)
        if self.meta_path.exists() and self.index_path.exists():
            with self.meta_path.open("r", encoding="utf-8") as f:
                meta = json.load(f)
            stored_model = meta.get("model")
            stored_dim = int(meta.get("dim", 0))
            if stored_model != self.model_name or stored_dim != self.dim:
                logger.warning(
                    "group %s index model/dim mismatch (stored=%s/%s current=%s/%s); starting empty — rebuild required",
                    self.group_id,
                    stored_model,
                    stored_dim,
                    self.model_name,
                    self.dim,
                )
                self.entries = []
                self.index = faiss.IndexFlatIP(self.dim)
                return
            self.entries = list(meta.get("entries") or [])
            self.index = faiss.read_index(str(self.index_path))
            if self.index.ntotal != len(self.entries):
                logger.warning(
                    "group %s index/meta size mismatch (%s vs %s); resetting",
                    self.group_id,
                    self.index.ntotal,
                    len(self.entries),
                )
                self.entries = []
                self.index = faiss.IndexFlatIP(self.dim)
        elif self.meta_path.exists() or self.index_path.exists():
            logger.warning("group %s incomplete index files; resetting", self.group_id)
            self.entries = []
            self.index = faiss.IndexFlatIP(self.dim)

    def _save_unlocked(self) -> None:
        self.dir.mkdir(parents=True, exist_ok=True)
        meta: dict[str, Any] = {
            "model": self.model_name,
            "dim": self.dim,
            "entries": self.entries,
        }
        tmp_meta = self.meta_path.with_suffix(".json.tmp")
        with tmp_meta.open("w", encoding="utf-8") as f:
            json.dump(meta, f, ensure_ascii=False, indent=2)
        tmp_meta.replace(self.meta_path)

        tmp_index = self.index_path.with_suffix(".faiss.tmp")
        faiss.write_index(self.index, str(tmp_index))
        tmp_index.replace(self.index_path)

    def attachment_ids(self) -> list[str]:
        with self._lock:
            return [e["attachment_id"] for e in self.entries]

    def _find_row(self, attachment_id: str) -> int | None:
        for i, e in enumerate(self.entries):
            if e["attachment_id"] == attachment_id:
                return i
        return None

    def _rebuild_without(self, drop_rows: set[int]) -> None:
        keep = [i for i in range(self.index.ntotal) if i not in drop_rows]
        if not keep:
            self.entries = []
            self.index = faiss.IndexFlatIP(self.dim)
            return
        vectors = np.vstack([self.index.reconstruct(i) for i in keep]).astype(np.float32)
        new_index = faiss.IndexFlatIP(self.dim)
        new_index.add(vectors)
        self.index = new_index
        self.entries = [self.entries[i] for i in keep]

    def upsert(self, attachment_id: str, entity_id: str, vector: np.ndarray) -> None:
        vec = np.asarray(vector, dtype=np.float32).reshape(1, -1)
        if vec.shape[1] != self.dim:
            raise ValueError(f"vector dim {vec.shape[1]} != {self.dim}")
        # Ensure L2-normalized for IndexFlatIP cosine semantics.
        norm = float(np.linalg.norm(vec))
        if norm > 0:
            vec = vec / norm

        with self._lock:
            existing = self._find_row(attachment_id)
            if existing is not None:
                self._rebuild_without({existing})
            self.index.add(vec)
            self.entries.append(
                {
                    "attachment_id": attachment_id,
                    "entity_id": entity_id,
                }
            )
            self._save_unlocked()

    def delete(self, attachment_id: str) -> bool:
        with self._lock:
            row = self._find_row(attachment_id)
            if row is None:
                return False
            self._rebuild_without({row})
            self._save_unlocked()
            return True

    def search(self, vector: np.ndarray, top_k: int) -> list[SearchHit]:
        vec = np.asarray(vector, dtype=np.float32).reshape(1, -1)
        norm = float(np.linalg.norm(vec))
        if norm > 0:
            vec = vec / norm

        with self._lock:
            n = self.index.ntotal
            if n == 0:
                return []
            k = min(max(top_k, 1), n)
            scores, indices = self.index.search(vec, k)
            hits: list[SearchHit] = []
            for score, idx in zip(scores[0].tolist(), indices[0].tolist()):
                if idx < 0 or idx >= len(self.entries):
                    continue
                e = self.entries[idx]
                hits.append(
                    SearchHit(
                        attachment_id=e["attachment_id"],
                        entity_id=e["entity_id"],
                        score=float(score),
                    )
                )
            return hits


class StoreManager:
    """Lazy per-group stores with a shared embedding dimension."""

    def __init__(self, dim: int, model_name: str, root: Path = config.DATA_DIR) -> None:
        self.dim = dim
        self.model_name = model_name
        self.root = root
        self.root.mkdir(parents=True, exist_ok=True)
        self._stores: dict[str, GroupStore] = {}
        self._lock = threading.Lock()

    def get(self, group_id: str) -> GroupStore:
        with self._lock:
            store = self._stores.get(group_id)
            if store is None:
                store = GroupStore(group_id, self.dim, self.model_name, self.root)
                self._stores[group_id] = store
            return store
