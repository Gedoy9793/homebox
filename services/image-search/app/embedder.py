"""DINOv2 image embedding via torch.hub (no Hugging Face Hub download)."""

from __future__ import annotations

import io
import logging
import os
from functools import lru_cache

import numpy as np
import torch
import torch.nn.functional as F
import torchvision.transforms as T
from PIL import Image

from . import config

logger = logging.getLogger(__name__)

# Official DINOv2 ImageNet normalization + 224 center crop.
_MEAN = (0.485, 0.456, 0.406)
_STD = (0.229, 0.224, 0.225)


class Embedder:
    """L2-normalized DINOv2 embeddings for cosine search via IndexFlatIP."""

    def __init__(self, model_name: str = config.MODEL_NAME, device: str = config.DEVICE) -> None:
        self.model_name = model_name
        self.device = torch.device(device)
        config.TORCH_HOME.mkdir(parents=True, exist_ok=True)
        os.environ["TORCH_HOME"] = str(config.TORCH_HOME)
        logger.info("Loading image model %s on %s via torch.hub", model_name, self.device)
        # Downloads from GitHub (facebookresearch/dinov2), not huggingface.co —
        # build/runtime environments that cannot reach HF still work.
        self.model = torch.hub.load(
            "facebookresearch/dinov2",
            model_name,
            trust_repo=True,
            verbose=False,
        )
        self.model.to(self.device)
        self.model.eval()
        self.transform = T.Compose(
            [
                T.Resize(256, interpolation=T.InterpolationMode.BICUBIC),
                T.CenterCrop(224),
                T.ToTensor(),
                T.Normalize(mean=_MEAN, std=_STD),
            ]
        )
        # ViT-S/14 → 384-d; probe once so StoreManager gets a real dim.
        with torch.inference_mode():
            probe = torch.zeros(1, 3, 224, 224, device=self.device)
            self.dim = int(self.model(probe).shape[-1])

    @torch.inference_mode()
    def embed_bytes(self, data: bytes) -> np.ndarray:
        image = Image.open(io.BytesIO(data)).convert("RGB")
        tensor = self.transform(image).unsqueeze(0).to(self.device)
        emb = self.model(tensor)
        emb = F.normalize(emb, p=2, dim=1)
        return emb.detach().cpu().numpy().astype(np.float32)


@lru_cache(maxsize=1)
def get_embedder() -> Embedder:
    return Embedder()
