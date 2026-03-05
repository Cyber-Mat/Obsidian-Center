import esbuild from "esbuild";
import process from "process";
import path from "path";

const prod = process.argv[2] === "production";

const buildOptions = {
  entryPoints: ["src/main.ts"],
  bundle: true,
  external: ["obsidian"],
  format: "cjs",
  target: "es2018",
  platform: "node",
  logLevel: "info",
  sourcemap: prod ? false : "inline",
  treeShaking: true,
  outfile: "main.js",
  minify: prod,
  // Force the base64-embedded WASM build so we don't need to load
  // .wasm files from disk (Obsidian plugin context).
  alias: {
    "@automerge/automerge": path.resolve(
      "node_modules/@automerge/automerge/dist/cjs/fullfat_base64.cjs"
    ),
  },
};

if (prod) {
  esbuild.build(buildOptions).catch(() => process.exit(1));
} else {
  const ctx = await esbuild.context(buildOptions);
  await ctx.watch();
}
