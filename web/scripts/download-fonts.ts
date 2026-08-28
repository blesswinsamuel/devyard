import { existsSync, mkdirSync, writeFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const FONTS_DIR = join(__dirname, "..", "src", "assets", "fonts");

const FONTS = [
  {
    name: "SymbolsNerdFontMono-Regular.ttf",
    url: "https://raw.githubusercontent.com/ryanoasis/nerd-fonts/master/patched-fonts/NerdFontsSymbolsOnly/SymbolsNerdFontMono-Regular.ttf",
  },
];

async function main() {
  mkdirSync(FONTS_DIR, { recursive: true });

  for (const font of FONTS) {
    const dest = join(FONTS_DIR, font.name);
    if (existsSync(dest) && statSync(dest).size > 1000) {
      console.log(`[fonts] ${font.name} already exists, skipping download.`);
      continue;
    }

    console.log(`[fonts] Downloading ${font.name}...`);
    try {
      const resp = await fetch(font.url);
      if (!resp.ok) {
        throw new Error(`Failed to fetch ${font.url}: ${resp.status} ${resp.statusText}`);
      }
      const buffer = Buffer.from(await resp.arrayBuffer());
      writeFileSync(dest, buffer);
      console.log(`[fonts] Successfully downloaded ${font.name} (${(buffer.length / 1024 / 1024).toFixed(2)} MB).`);
    } catch (err) {
      console.warn(`[fonts] Warning: Could not download ${font.name}:`, err);
    }
  }
}

void main();
