import fs from "node:fs"
import path from "node:path"
import process from "node:process"
import { spawnSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const args = process.argv.slice(2)
if (args.length > 1 || (args[0] && args[0] !== "--update")) {
  throw new Error("usage: node scripts/ci/security/gosec-baseline.mjs [--update]")
}
const mode = args[0]
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..")
const baselinePath = path.join(root, "scripts", "ci", "security", "gosec-baseline.json")
const gosec = process.env.GOSEC || "gosec"
const scan = spawnSync(gosec, ["-quiet", "-fmt=json", "./..."], {
  cwd: root,
  encoding: "utf8",
  windowsHide: true,
  maxBuffer: 32 * 1024 * 1024,
})
if (scan.error) throw scan.error
if (!scan.stdout.trim()) {
  throw new Error(`gosec produced no JSON report${scan.stderr.trim() ? `: ${scan.stderr.trim()}` : ""}`)
}

const normalizeCode = (value = "") => value.split("\n").map((line) => line.replace(/^\s*\d+:\s?/, "")).join("\n").trim()
const normalize = (issue) => ({
  rule_id: issue.rule_id,
  severity: issue.severity,
  confidence: issue.confidence,
  file: path.relative(root, issue.file).split(path.sep).join("/"),
  details: issue.details,
  code: normalizeCode(issue.code),
})
const sortIssues = (issues) => issues.map(normalize).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)))
const report = JSON.parse(scan.stdout)
const current = sortIssues(report.Issues || [])

if (mode === "--update") {
  fs.writeFileSync(baselinePath, `${JSON.stringify(current, null, 2)}\n`)
  console.log(`Updated gosec baseline with ${current.length} baselined findings.`)
  process.exit(0)
}

const baseline = JSON.parse(fs.readFileSync(baselinePath, "utf8"))
const key = (value) => JSON.stringify(value)
const currentCounts = new Map()
const baselineCounts = new Map()
for (const issue of current) currentCounts.set(key(issue), (currentCounts.get(key(issue)) || 0) + 1)
for (const issue of baseline) baselineCounts.set(key(issue), (baselineCounts.get(key(issue)) || 0) + 1)
const expandDiff = (left, right) => {
  const out = []
  for (const [fingerprint, count] of left) for (let i = right.get(fingerprint) || 0; i < count; i++) out.push(JSON.parse(fingerprint))
  return out
}
const added = expandDiff(currentCounts, baselineCounts)
const resolved = expandDiff(baselineCounts, currentCounts)
if (added.length === 0 && resolved.length === 0) {
  console.log(`gosec baseline matches ${current.length} baselined findings; no new findings.`)
  process.exit(0)
}
const print = (title, issues) => {
  if (issues.length === 0) return
  console.error(`${title} (${issues.length}):`)
  for (const issue of issues) console.error(`- ${issue.rule_id} ${issue.file}: ${issue.details}`)
}
print("New gosec findings", added)
print("Resolved/stale baseline findings", resolved)
console.error("Review the delta, then regenerate intentionally with --update.")
process.exit(1)
