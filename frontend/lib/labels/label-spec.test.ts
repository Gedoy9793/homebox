import { describe, expect, it } from "vitest";
import { LABEL_SPEC_KEYWORD, parseLabelSpec, readLabelSpecFromPng } from "./label-spec";

const PNG_SIGNATURE = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

function ascii(value: string): Uint8Array {
  return Uint8Array.from(value, char => char.charCodeAt(0));
}

/** Builds a PNG chunk. The reader walks chunks by length, so the CRC stays zero. */
function chunk(type: string, ...parts: Uint8Array[]): Uint8Array {
  const data = parts.reduce<number[]>((all, part) => all.concat(Array.from(part)), []);
  const out = new Uint8Array(12 + data.length);
  new DataView(out.buffer).setUint32(0, data.length);
  out.set(ascii(type), 4);
  out.set(data, 8);
  return out;
}

function png(...chunks: Uint8Array[]): ArrayBuffer {
  const all = [PNG_SIGNATURE, chunk("IHDR", new Uint8Array(13)), ...chunks, chunk("IEND")];
  const size = all.reduce((total, part) => total + part.length, 0);
  const out = new Uint8Array(size);
  let offset = 0;
  for (const part of all) {
    out.set(part, offset);
    offset += part.length;
  }
  return out.buffer;
}

function iTXt(keyword: string, text: string): Uint8Array {
  // keyword \0 compressionFlag compressionMethod language \0 translated \0 text
  return chunk(
    "iTXt",
    ascii(keyword),
    new Uint8Array([0, 0, 0]),
    new Uint8Array([0]),
    new Uint8Array([0]),
    new TextEncoder().encode(text)
  );
}

async function deflate(value: string): Promise<Uint8Array> {
  const stream = new Blob([new TextEncoder().encode(value)]).stream().pipeThrough(new CompressionStream("deflate"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

const minimal = { width: 40, height: 30, items: [] };

describe("parseLabelSpec", () => {
  it("keeps every supported item type", () => {
    const spec = parseLabelSpec({
      width: 40,
      height: 30,
      rotation: 90,
      gapType: 2,
      printSpeed: 3,
      printDarkness: 8,
      threshold: 192,
      items: [
        { type: "text", text: "Drill", x: 20, y: 1, width: 19, fontHeight: 3.5, bold: true, align: "center" },
        { type: "qrcode", text: "https://example.test/item/1", x: 1, y: 1, width: 18 },
        { type: "barcode", text: "123456", barcodeType: "CODE128", textHeight: 3 },
        { type: "line", x1: 0, y1: 15, x2: 40, y2: 15, lineWidth: 0.3 },
        { type: "rect", x: 0, y: 0, width: 40, height: 30, cornerWidth: 1 },
        { type: "ellipse", x: 5, y: 5, width: 4, height: 4, fill: true },
        { type: "circle", x: 10, y: 10, radius: 2 },
        { type: "image", src: "data:image/png;base64,AAAA" },
      ],
    });

    expect(spec.items.map(item => item.type)).toEqual([
      "text",
      "qrcode",
      "barcode",
      "line",
      "rect",
      "ellipse",
      "circle",
      "image",
    ]);
    expect(spec.rotation).toBe(90);
  });

  it("clamps the copy count into a printable range", () => {
    expect(parseLabelSpec({ ...minimal, copies: 500 }).copies).toBe(99);
    expect(parseLabelSpec({ ...minimal, copies: 0 }).copies).toBe(1);
    expect(parseLabelSpec(minimal).copies).toBeUndefined();
  });

  it("rejects a label without a usable size", () => {
    expect(() => parseLabelSpec({ height: 30, items: [] })).toThrow(/width/);
    expect(() => parseLabelSpec({ width: 0, height: 30, items: [] })).toThrow(/width/);
    expect(() => parseLabelSpec({ width: 40, height: 999999, items: [] })).toThrow(/height/);
  });

  it("names the offending item when it cannot be drawn", () => {
    expect(() => parseLabelSpec({ ...minimal, items: [{ type: "hologram" }] })).toThrow(/items\[0\]\.type/);
    expect(() => parseLabelSpec({ ...minimal, items: [{ type: "text" }] })).toThrow(/items\[0\]\.text/);
    expect(() => parseLabelSpec({ ...minimal, items: [{ type: "text", text: "a", x: "1" }] })).toThrow(/items\[0\]\.x/);
  });

  it("rejects cross-origin images, which would taint the print canvas", () => {
    expect(() =>
      parseLabelSpec({ ...minimal, items: [{ type: "image", src: "https://cdn.example.test/logo.png" }] })
    ).toThrow(/same-origin/);
  });
});

describe("readLabelSpecFromPng", () => {
  it("reads a layout from an uncompressed iTXt chunk", async () => {
    const spec = await readLabelSpecFromPng(png(iTXt(LABEL_SPEC_KEYWORD, JSON.stringify(minimal))));
    expect(spec).toMatchObject({ width: 40, height: 30 });
  });

  it("reads a layout from a compressed iTXt chunk", async () => {
    const compressed = chunk(
      "iTXt",
      ascii(LABEL_SPEC_KEYWORD),
      new Uint8Array([0, 1, 0]),
      new Uint8Array([0]),
      new Uint8Array([0]),
      await deflate(JSON.stringify(minimal))
    );

    expect(await readLabelSpecFromPng(png(compressed))).toMatchObject({ width: 40, height: 30 });
  });

  it("reads a base64 layout from a Latin-1 only tEXt chunk", async () => {
    const json = JSON.stringify({ ...minimal, items: [{ type: "text", text: "锤子" }] });
    const encoded = btoa(String.fromCharCode(...new TextEncoder().encode(json)));
    const text = chunk("tEXt", ascii(LABEL_SPEC_KEYWORD), new Uint8Array([0]), ascii(encoded));

    const spec = await readLabelSpecFromPng(png(text));
    expect(spec?.items[0]).toMatchObject({ type: "text", text: "锤子" });
  });

  it("returns nothing for images without a layout", async () => {
    expect(await readLabelSpecFromPng(png(iTXt("Comment", "just a picture")))).toBeUndefined();
    expect(await readLabelSpecFromPng(new TextEncoder().encode("not a png").buffer)).toBeUndefined();
  });

  it("reports why a broken layout cannot be printed", async () => {
    await expect(readLabelSpecFromPng(png(iTXt(LABEL_SPEC_KEYWORD, '{"width":40}')))).rejects.toThrow(/items/);
  });
});

// Captured from backend/localsvc, the label service bundled with the server.
// Both sides have to agree on this JSON, and only one of them is written in
// TypeScript, so the real output is pinned here.
describe("the bundled label service", () => {
  const emitted = {
    width: 25,
    height: 15,
    items: [
      {
        type: "qrcode",
        x: 1,
        y: 1,
        width: 10,
        height: 10,
        text: "https://homebox.example.com/a/000-042",
      },
      {
        type: "text",
        x: 12.2,
        y: 1,
        width: 11.8,
        height: 3.5,
        text: "000-042",
        fontHeight: 2.8,
        bold: true,
        valign: "center",
        wrap: "none",
      },
      {
        type: "text",
        x: 12.2,
        y: 4.5,
        width: 11.8,
        height: 2.5,
        text: "Netgear",
        fontHeight: 2,
        valign: "center",
        wrap: "none",
      },
    ],
  };

  it("emits a layout this parser accepts", () => {
    const spec = parseLabelSpec(emitted);

    expect(spec).toMatchObject({ width: 25, height: 15 });
    expect(spec.items).toHaveLength(3);
    expect(spec.items[0]).toMatchObject({ type: "qrcode", text: emitted.items[0].text });
    expect(spec.items[1]).toMatchObject({ type: "text", text: "000-042", bold: true, fontHeight: 2.8 });
  });

  // Wrapping is resolved server-side into one item per line, so the printer must
  // not re-wrap and drift away from the preview.
  it("keeps every line as its own item", () => {
    for (const item of parseLabelSpec(emitted).items) {
      if (item.type === "text") {
        expect(item.wrap).toBe("none");
        expect(item.text).not.toContain("\n");
      }
    }
  });

  // A cable flag is a 25x38mm label printed sideways, so the canvas arrives with
  // the sides swapped and a rotation the printer has to apply. The fold that
  // splits the two faces is drawn on the preview only, never sent to print.
  it("carries the rotation of a sideways label and no fold line", () => {
    const spec = parseLabelSpec({
      width: 38,
      height: 25,
      rotation: 90,
      items: [
        { type: "qrcode", x: 1, y: 1, width: 10.5, height: 10.5, text: "https://homebox.example.com/item/abc" },
        {
          type: "text",
          x: 12.7,
          y: 1,
          width: 24.3,
          height: 3.75,
          text: "SW1-P24",
          fontHeight: 3,
          bold: true,
          valign: "center",
          wrap: "none",
        },
      ],
    });

    expect(spec.rotation).toBe(90);
    expect(spec.items.some(item => item.type === "line")).toBe(false);
  });
});
