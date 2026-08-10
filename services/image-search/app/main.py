"""FastAPI entrypoint for the Homebox image-search sidecar."""

from __future__ import annotations

import logging
from contextlib import asynccontextmanager
from typing import Annotated

from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from pydantic import BaseModel, Field

from . import config
from .embedder import Embedder, get_embedder
from .store import StoreManager

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s [%(name)s] %(message)s",
)
logger = logging.getLogger("image-search")

store_manager: StoreManager | None = None
embedder: Embedder | None = None


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global store_manager, embedder
    config.DATA_DIR.mkdir(parents=True, exist_ok=True)
    embedder = get_embedder()
    store_manager = StoreManager(dim=embedder.dim, model_name=embedder.model_name)
    logger.info(
        "image-search ready model=%s dim=%s data_dir=%s",
        embedder.model_name,
        embedder.dim,
        config.DATA_DIR,
    )
    yield
    store_manager = None
    embedder = None


app = FastAPI(title="Homebox Image Search", version="1.0.0", lifespan=lifespan)


class IndexResponse(BaseModel):
    ok: bool = True
    group_id: str
    attachment_id: str
    entity_id: str


class DeleteResponse(BaseModel):
    ok: bool
    group_id: str
    attachment_id: str


class IdsResponse(BaseModel):
    group_id: str
    attachment_ids: list[str]


class SearchResult(BaseModel):
    attachment_id: str
    entity_id: str
    score: float


class SearchResponse(BaseModel):
    results: list[SearchResult] = Field(default_factory=list)


class HealthResponse(BaseModel):
    status: str
    model: str
    dim: int


def _require_ready() -> tuple[Embedder, StoreManager]:
    if embedder is None or store_manager is None:
        raise HTTPException(status_code=503, detail="service not ready")
    return embedder, store_manager


async def _read_upload(file: UploadFile) -> bytes:
    data = await file.read()
    if not data:
        raise HTTPException(status_code=400, detail="empty file")
    return data


@app.get("/healthz", response_model=HealthResponse)
def healthz() -> HealthResponse:
    emb, _ = _require_ready()
    return HealthResponse(status="ok", model=emb.model_name, dim=emb.dim)


@app.post("/v1/index", response_model=IndexResponse)
async def index_image(
    group_id: Annotated[str, Form()],
    attachment_id: Annotated[str, Form()],
    entity_id: Annotated[str, Form()],
    file: Annotated[UploadFile, File()],
) -> IndexResponse:
    emb, stores = _require_ready()
    if not group_id or not attachment_id or not entity_id:
        raise HTTPException(status_code=400, detail="group_id, attachment_id, and entity_id are required")
    data = await _read_upload(file)
    try:
        vector = emb.embed_bytes(data)
        stores.get(group_id).upsert(attachment_id, entity_id, vector)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:  # noqa: BLE001 — surface embed/decode errors to caller
        logger.exception("index failed group=%s attachment=%s", group_id, attachment_id)
        raise HTTPException(status_code=400, detail=f"failed to index image: {exc}") from exc
    return IndexResponse(
        group_id=group_id,
        attachment_id=attachment_id,
        entity_id=entity_id,
    )


@app.delete("/v1/index/{group_id}/{attachment_id}", response_model=DeleteResponse)
def delete_index(group_id: str, attachment_id: str) -> DeleteResponse:
    _, stores = _require_ready()
    try:
        deleted = stores.get(group_id).delete(attachment_id)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return DeleteResponse(ok=deleted, group_id=group_id, attachment_id=attachment_id)


@app.get("/v1/index/{group_id}/ids", response_model=IdsResponse)
def list_ids(group_id: str) -> IdsResponse:
    _, stores = _require_ready()
    try:
        ids = stores.get(group_id).attachment_ids()
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return IdsResponse(group_id=group_id, attachment_ids=ids)


@app.post("/v1/search", response_model=SearchResponse)
async def search_images(
    group_id: Annotated[str, Form()],
    file: Annotated[UploadFile, File()],
    top_k: Annotated[int, Form()] = config.DEFAULT_TOP_K,
) -> SearchResponse:
    emb, stores = _require_ready()
    if not group_id:
        raise HTTPException(status_code=400, detail="group_id is required")
    if top_k < 1:
        raise HTTPException(status_code=400, detail="top_k must be >= 1")
    top_k = min(top_k, config.MAX_TOP_K)
    data = await _read_upload(file)
    try:
        vector = emb.embed_bytes(data)
        hits = stores.get(group_id).search(vector, top_k)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:  # noqa: BLE001
        logger.exception("search failed group=%s", group_id)
        raise HTTPException(status_code=400, detail=f"failed to search image: {exc}") from exc
    return SearchResponse(
        results=[
            SearchResult(
                attachment_id=h.attachment_id,
                entity_id=h.entity_id,
                score=h.score,
            )
            for h in hits
        ]
    )
