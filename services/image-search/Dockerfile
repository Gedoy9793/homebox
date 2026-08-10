# Homebox image-search sidecar: DINOv2 + FAISS (CPU).
# Model weights come from torch.hub (GitHub), not Hugging Face — GHA and many
# networks can reach github.com when huggingface.co is blocked or gated.
FROM python:3.11-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    IMAGE_SEARCH_DATA_DIR=/var/lib/image-search \
    IMAGE_SEARCH_MODEL=dinov2_vits14 \
    IMAGE_SEARCH_HOST=0.0.0.0 \
    IMAGE_SEARCH_PORT=8080 \
    TORCH_HOME=/opt/torch-cache

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        git \
        gosu \
        libgomp1 \
        libgl1 \
        libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --upgrade pip \
    && pip install --index-url https://download.pytorch.org/whl/cpu torch torchvision \
    && pip install -r requirements.txt

COPY app ./app
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Prefetch DINOv2 via torch.hub (clones facebookresearch/dinov2 from GitHub).
RUN mkdir -p /opt/torch-cache \
    && python - <<'PY'
import os
import torch

os.environ.setdefault("TORCH_HOME", "/opt/torch-cache")
name = os.environ.get("IMAGE_SEARCH_MODEL", "dinov2_vits14")
model = torch.hub.load("facebookresearch/dinov2", name, trust_repo=True)
print("prefetched", name, "dim", model(torch.zeros(1, 3, 224, 224)).shape[-1])
PY

RUN mkdir -p /var/lib/image-search \
    && useradd --create-home --uid 1000 --shell /usr/sbin/nologin imagesearch \
    && chown -R imagesearch:imagesearch /var/lib/image-search /opt/torch-cache

# Stay root for entrypoint so it can chown the mounted volume, then drop to
# imagesearch. Do not set USER here.
VOLUME ["/var/lib/image-search"]
EXPOSE 8080

# First boot may still warm caches; allow a longer start window.
HEALTHCHECK --interval=30s --timeout=5s --start-period=180s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=3)"

ENTRYPOINT ["/entrypoint.sh"]
CMD ["python", "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8080"]
