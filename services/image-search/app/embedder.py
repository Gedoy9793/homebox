"""DINOv2 image embedding."""

from __future__ import annotations

import io
import logging
from functools import lru_cache

import numpy as np
import torch
import torch.nn.functional as F
from PIL import Image
from transformers import AutoImageProcessor, AutoModel

from . import config

logger = logging.getLogger(__name__)


class Embedder:
    """L2-normalized DINOv2 embeddings for cosine search via IndexFlatIP."""

    def __init__(self, model_name: str = config.MODEL_NAME, device: str = config.DEVICE) -> None:
        self.model_name = model_name
        self.device = torch.device(device)
        logger.info("Loading image model %s on %s", model_name, self.device)
        self.processor = AutoImageProcessor.from_pretrained(model_name)
        self.model = AutoModel.from_pretrained(model_name)
        self.model.to(self.device)
        self.model.eval()
        self.dim = int(self.model.config.hidden_size)

    @torch.inference_mode()
    def embed_bytes(self, data: bytes) -> np.ndarray:
        image = Image.open(io.BytesIO(data)).convert("RGB")
        inputs = self.processor(images=image, return_tensors="pt")
        inputs = {k: v.to(self.device) for k, v in inputs.items()}
        outputs = self.model(**inputs)
        # CLS token embedding, L2-normalized → cosine via inner product.
        emb = outputs.last_hidden_state[:, 0, :]
        emb = F.normalize(emb, p=2, dim=1)
        return emb.detach().cpu().numpy().astype(np.float32)


@lru_cache(maxsize=1)
def get_embedder() -> Embedder:
    return Embedder()
