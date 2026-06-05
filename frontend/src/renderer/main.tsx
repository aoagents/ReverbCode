import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles/theme.css"; // design tokens + Tailwind (must load first)
import "./styles.css"; // component styles (consume the tokens)

const container = document.getElementById("root");
if (!container) throw new Error("root element missing");

createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
