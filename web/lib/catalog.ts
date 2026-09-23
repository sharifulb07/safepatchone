// Generated at build time by Go. Do not maintain plan or feature lists here.
import data from "../.generated/product-catalog.json";

export type CatalogFeature = {
  key: string; name: string; description: string; category: string;
  availability: string; limit_value: number | null;
};
export type CatalogEdition = {
  key: string; name: string; description: string; edition_version_id: string;
  price_label: string; price_note: string; trial_days: number | null; trial_note: string;
  action: { label: string; href: string; unavailable_message: string };
  features: CatalogFeature[];
};
export type ProductCatalog = { release: string; editions: CatalogEdition[] };
export const catalog: ProductCatalog = data;
