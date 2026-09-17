// This is the very first code that runs in the browser: it finds the
// empty <div id="root"> declared in index.html and tells React to render
// the whole app into it.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./index.css";

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("index.html is missing the #root element React renders into");
}

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>
);
