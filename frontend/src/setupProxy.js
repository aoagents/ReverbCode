const { createProxyMiddleware } = require("http-proxy-middleware");

const docsTarget = process.env.DOCS_DEV_SERVER || "http://127.0.0.1:3003";

module.exports = function setupProxy(app) {
  app.use(
    ["/docs", "/_next"],
    createProxyMiddleware({
      target: docsTarget,
      changeOrigin: true,
      ws: true,
    })
  );
};
