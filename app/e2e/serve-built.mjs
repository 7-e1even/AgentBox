import { cpSync, existsSync, mkdirSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const appRoot = fileURLToPath(new URL("..", import.meta.url))
const standaloneRoot = join(appRoot, ".next", "standalone")
const serverPath = join(standaloneRoot, "server.js")

if (!existsSync(serverPath)) {
  throw new Error(
    "Missing standalone build. Run `pnpm build` before the E2E suite."
  )
}

function copyDirectory(source, destination) {
  if (!existsSync(source)) return

  mkdirSync(dirname(destination), { recursive: true })
  cpSync(source, destination, { recursive: true, force: true })
}

copyDirectory(
  join(appRoot, ".next", "static"),
  join(standaloneRoot, ".next", "static")
)
copyDirectory(join(appRoot, "public"), join(standaloneRoot, "public"))

await import(pathToFileURL(serverPath).href)
