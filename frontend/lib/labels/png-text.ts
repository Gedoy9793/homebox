// Reader for the textual metadata chunks of a PNG file (tEXt, zTXt, iTXt).
//
// Homebox uses them to ship a label layout alongside the rendered label
// preview: the same request returns both a picture a human can look at and the
// machine-readable description a label printer can re-draw at its own
// resolution. See lib/labels/label-spec.ts for the payload format.

const PNG_SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];

// A label layout is a few kilobytes; anything larger is not ours to read.
const MAX_CHUNK_SIZE = 1 << 20;

export interface PngTextChunk {
  keyword: string;
  text: string;
}

const latin1Decoder = new TextDecoder("latin1");
const utf8Decoder = new TextDecoder("utf-8");
const strictUtf8Decoder = new TextDecoder("utf-8", { fatal: true });

/**
 * Decodes a PNG text value. The spec says tEXt/zTXt are Latin-1, but encoders
 * in the wild happily write UTF-8 there, and item names are rarely ASCII, so
 * prefer UTF-8 and fall back to Latin-1 when it isn't valid UTF-8.
 */
function decodeText(bytes: Uint8Array): string {
  try {
    return strictUtf8Decoder.decode(bytes);
  } catch {
    return latin1Decoder.decode(bytes);
  }
}

async function inflate(bytes: Uint8Array): Promise<Uint8Array> {
  if (typeof DecompressionStream === "undefined") {
    throw new Error("this browser cannot decompress PNG metadata");
  }

  const stream = new Blob([bytes as BlobPart]).stream().pipeThrough(new DecompressionStream("deflate"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

function splitAtNull(bytes: Uint8Array, from = 0): [Uint8Array, number] {
  const end = bytes.indexOf(0, from);
  if (end === -1) {
    return [bytes.subarray(from), bytes.length];
  }
  return [bytes.subarray(from, end), end + 1];
}

async function readChunk(type: string, data: Uint8Array): Promise<PngTextChunk | undefined> {
  const [keywordBytes, afterKeyword] = splitAtNull(data);
  const keyword = latin1Decoder.decode(keywordBytes);

  switch (type) {
    case "tEXt":
      return { keyword, text: decodeText(data.subarray(afterKeyword)) };

    case "zTXt": {
      // A single compression-method byte precedes the compressed value.
      const text = await inflate(data.subarray(afterKeyword + 1));
      return { keyword, text: decodeText(text) };
    }

    case "iTXt": {
      const compressed = data[afterKeyword] === 1;
      // Skip the compression flag, compression method, language tag and
      // translated keyword to reach the UTF-8 value.
      const [, afterLanguage] = splitAtNull(data, afterKeyword + 2);
      const [, afterTranslated] = splitAtNull(data, afterLanguage);
      const value = data.subarray(afterTranslated);
      return { keyword, text: utf8Decoder.decode(compressed ? await inflate(value) : value) };
    }

    default:
      return undefined;
  }
}

/**
 * Reads every text chunk of a PNG. Returns an empty list for data that isn't a
 * PNG at all, so callers can treat "no metadata" and "not a PNG" alike.
 */
export async function readPngTextChunks(buffer: ArrayBuffer): Promise<PngTextChunk[]> {
  const bytes = new Uint8Array(buffer);
  if (bytes.length < PNG_SIGNATURE.length || PNG_SIGNATURE.some((byte, i) => bytes[i] !== byte)) {
    return [];
  }

  const view = new DataView(buffer);
  const chunks: PngTextChunk[] = [];

  // Every chunk is length(4) + type(4) + data(length) + crc(4).
  let offset = PNG_SIGNATURE.length;
  while (offset + 12 <= bytes.length) {
    const length = view.getUint32(offset);
    const type = latin1Decoder.decode(bytes.subarray(offset + 4, offset + 8));
    const dataStart = offset + 8;
    const dataEnd = dataStart + length;

    if (type === "IEND" || dataEnd + 4 > bytes.length) {
      break;
    }

    if (length <= MAX_CHUNK_SIZE) {
      const chunk = await readChunk(type, bytes.subarray(dataStart, dataEnd));
      if (chunk) {
        chunks.push(chunk);
      }
    }

    offset = dataEnd + 4;
  }

  return chunks;
}
