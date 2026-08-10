# Homebox image-search sidecar: DINOv2 + FAISS (CPU).
# Model weights come from torch.hub (GitHub), not Hugging Face — GHA and many
# networks can reach github.com when huggingface.co is blocked or gated.
FROM python:3.11-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    IMAGE_SEARCH_DATA_DIR=/var/lib/image-search \
    IMAGE_SEARCH_MODEL=dinov2_vitg14 \
    IMAGE_SEARCH_HOST=0.0.0.0 \
    IMAGE_SEARCH_PORT=8080 \
    # Keep weights on the data volume (not the image) — giant is ~several GB.
    TORCH_HOME=/var/lib/image-search/.torch

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

# Do not prefetch dinov2_vitg14 at build time: weights are multi-GB and will OOM
# or timeout CI. First container start downloads into TORCH_HOME on the volume.

RUN mkdir -p /var/lib/image-search \
    && useradd --create-home --uid 1000 --shell /usr/sbin/nologin imagesearch \
    && chown -R imagesearch:imagesearch /var/lib/image-search

# Stay root for entrypoint so it can chown the mounted volume, then drop to
# imagesearch. Do not set USER here.
VOLUME ["/var/lib/image-search"]
EXPOSE 8080

# First boot may download giant weights + load the model (can take several minutes).
HEALTHCHECK --interval=30s --timeout=5s --start-period=600s --retries=5 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=3)"

ENTRYPOINT ["/entrypoint.sh"]
CMD ["python", "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8080"]
