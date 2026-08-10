import { BaseAPI, route } from "../base";
import type { EntitySummary } from "../types/data-contracts";

export type ImageSearchHit = EntitySummary & { score?: number };

export class ImageSearchAPI extends BaseAPI {
  async searchByImage(file: File | Blob, filename = "query.jpg") {
    const formData = new FormData();
    formData.append("file", file, filename);

    // Backend returns Results[ImageSearchHit] → { items: [...] }
    const resp = await this.http.post<FormData, { items: ImageSearchHit[] }>({
      url: route("/entities/search-by-image"),
      data: formData,
    });
    return {
      ...resp,
      data: resp.data?.items ?? [],
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as { data: ImageSearchHit[]; error: any; status: number };
  }

  rebuildIndex() {
    return this.http.post<void, { completed: number }>({
      url: route("/actions/rebuild-image-index"),
    });
  }
}
