import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";
import { App } from "./app";
import { initEmbed } from "./lib/embed";
import { initTheme } from "./lib/theme";

initTheme();
initEmbed();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
