# Homebox image-search sidecar: DINOv2 + FAISS (CPU).
FROM python:3.11-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    IMAGE_SEARCH_DATA_DIR=/var/lib/image-search \
    IMAGE_SEARCH_MODEL=facebook/dinov2-vits14 \
    IMAGE_SEARCH_HOST=0.0.0.0 \
    IMAGE_SEARCH_PORT=8080 \
    HF_HOME=/opt/hf-cache \
    TRANSFORMERS_CACHE=/opt/hf-cache

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libgomp1 \
        libgl1 \
        libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# CPU torch first, then the rest of the Python deps.
COPY requirements.txt .
RUN pip install --upgrade pip \
    && pip install --index-url https://download.pytorch.org/whl/cpu torch \
    && pip install -r requirements.txt

COPY app ./app

# Pre-pull the DINOv2 weights into the image for faster cold start.
RUN python - <<'PY'
from transformers import AutoImageProcessor, AutoModel
import os
name = os.environ["IMAGE_SEARCH_MODEL"]
AutoImageProcessor.from_pretrained(name)
AutoModel.from_pretrained(name)
print("prefetched", name)
PY

RUN mkdir -p /var/lib/image-search \
    && useradd --create-home --uid 1000 --shell /usr/sbin/nologin imagesearch \
    && chown -R imagesearch:imagesearch /var/lib/image-search /opt/hf-cache

USER imagesearch
VOLUME ["/var/lib/image-search"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=3)"

CMD ["python", "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8080"]
