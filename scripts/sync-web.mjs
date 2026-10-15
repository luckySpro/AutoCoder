// 把 web/dist 同步到 internal/server/dist（go:embed 目录），并清理旧的 hash 资源。
import { cpSync, existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const src = join(root, 'web', 'dist')
const dst = join(root, 'internal', 'server', 'dist')
if (!existsSync(join(src, 'index.html'))) {
  console.error('web/dist 不存在，请先运行 npm --prefix web run build')
  process.exit(1)
}
rmSync(dst, { recursive: true, force: true })
mkdirSync(dst, { recursive: true })
cpSync(src, dst, { recursive: true })
writeFileSync(join(dst, '.keep'), '前端构建产物由 make web 同步到此目录\n')
console.log('已同步', src, '→', dst)
