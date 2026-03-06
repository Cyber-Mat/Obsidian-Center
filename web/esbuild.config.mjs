import esbuild from "esbuild";
import process from "process";
import path from "path";

const prod = process.argv[2] === "production";

const buildOptions = {
  entryPoints: ["src/main.ts"],
  bundle: true,
  format: "esm",
  target: "es2020",
  platform: "browser",
  logLevel: "info",
  sourcemap: prod ? false : "inline",
  treeShaking: true,
  outfile: "dist/app.js",
  minify: prod,
  // Force the base64-embedded WASM build for browser context
  alias: {
    "@automerge/automerge": path.resolve(
      "node_modules/@automerge/automerge/dist/mjs/entrypoints/fullfat_base64.js"
    ),
  },
};

if (prod) {
  esbuild.build(buildOptions).catch(() => process.exit(1));
} else {
  const ctx = await esbuild.context(buildOptions);
  await ctx.watch();
}
