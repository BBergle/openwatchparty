import { fileURLToPath } from "node:url";

// Compiled to build/test/paths.js: four levels up is the repository root.
export const REPO = fileURLToPath(new URL("../../../../", import.meta.url));
export const VECTORS = `${REPO}conformance/vectors/`;
export const RELAY_DIR = `${REPO}relay/`;
