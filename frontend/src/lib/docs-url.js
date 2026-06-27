const DEFAULT_DOCS_URL = "/docs";

export function docsUrl(path = "") {
  const base = (process.env.REACT_APP_DOCS_URL || DEFAULT_DOCS_URL).replace(/\/$/, "");
  const cleanPath = path ? `/${path.replace(/^\/+/, "")}` : "";
  return `${base}${cleanPath}`;
}
