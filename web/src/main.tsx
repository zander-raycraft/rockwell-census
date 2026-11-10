import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles.css";

/**
 * Rockwell mounts the company document workspace.
 * @brief: Rockwell -> starts the React application
 * @returns: root [void] -> mounted workspace
 */
const root = document.getElementById("root");
if (!root) throw new Error("Application root is missing.");
createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
