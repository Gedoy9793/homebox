"""Runtime configuration for the image-search sidecar."""

from __future__ import annotations

import os
from pathlib import Path

# torch.hub entry under facebookresearch/dinov2 (e.g. dinov2_vits14).
MODEL_NAME = os.environ.get("IMAGE_SEARCH_MODEL", "dinov2_vits14")
DATA_DIR = Path(os.environ.get("IMAGE_SEARCH_DATA_DIR", "/var/lib/image-search"))
HOST = os.environ.get("IMAGE_SEARCH_HOST", "0.0.0.0")
PORT = int(os.environ.get("IMAGE_SEARCH_PORT", "8080"))
DEFAULT_TOP_K = int(os.environ.get("IMAGE_SEARCH_DEFAULT_TOP_K", "20"))
MAX_TOP_K = int(os.environ.get("IMAGE_SEARCH_MAX_TOP_K", "100"))
DEVICE = os.environ.get("IMAGE_SEARCH_DEVICE", "cpu")
# Where torch.hub caches the cloned repo + weights.
TORCH_HOME = Path(os.environ.get("TORCH_HOME", str(DATA_DIR / ".torch")))
