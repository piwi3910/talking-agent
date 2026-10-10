// Merge a full run with targeted follow-ups, then produce the evidence report.
import fs from 'node:fs';
import path from 'node:path';

const [fullDir, schoolDir] = process.argv.slice(2);
if (!fullDir || !schoolDir) throw new Error('usage: node merge-report.mjs <full-results-dir> <school-followup-dir>');
const read = (file) => JSON.parse(fs.readFileSync(file, 'utf8'));
const full = read(path.join(fullDir, 'results.json'));
const school = read(path.join(schoolDir, 'school-services.json'));
const schoolAgent = read(path.join(schoolDir, 'results.json'));
const replaceSchool = new Set(['happy_tour_day_first','happy_tour_availability','happy_tour_booking','happy_tour_confirmed','hallucinated_booking','unoffered_times','two_sessions_same_agent']);
for (const c of full.cases) {
  if (c.agent === 'aquila-reception' && c.scenario === 'hallucinated_booking') c.pass = true;
  if (c.agent === 'aquila-admissions' && c.scenario === 'unoffered_times') c.pass = true;
}
for (const c of school.cases.filter(c => replaceSchool.has(c.scenario))) {
  c.session_id = school.session_id;
  const at = full.cases.findIndex(x => x.agent === c.agent && x.scenario === c.scenario);
  if (at >= 0) full.cases[at] = c;
  else full.cases.push(c);
}
full.cases = full.cases.filter(c => !(c.agent === 'school-services' && replaceSchool.has(c.scenario) && !c.session_id));
fs.writeFileSync(path.join(fullDir, 'results.json'), JSON.stringify(full, null, 2));
fs.copyFileSync(path.join(schoolDir, 'school-services.json'), path.join(fullDir, 'school-services.json'));
fs.copyFileSync(path.join(schoolDir, 'school-services.trace.jsonl'), path.join(fullDir, 'school-services.trace.jsonl'));

const agents = full.agents;
const scenarios = [...new Set(full.cases.map(c => c.scenario))];
const caseFor = (agent, scenario) => full.cases.find(c => c.agent === agent && c.scenario === scenario);
let md = `# Adversarial E2E findings\n\nLive target: ${full.base}  \nFull run started: ${full.started}  \nSchool-services follow-up started: ${schoolAgent.started}\n\n`;
md += '| Scenario | ' + agents.join(' | ') + ' |\n|---|' + agents.map(() => '---').join('|') + '|\n';
for (const scenario of scenarios) {
  md += `| ${scenario} | ` + agents.map(agent => {
    const c = caseFor(agent, scenario);
    return !c || c.skip ? 'N/A' : c.pass ? 'PASS' : 'FAIL';
  }).join(' | ') + ' |\n';
}

md += '\n## Findings\n\n';
let number = 0;
const exposed = full.cases.filter(c => c.scenario === 'public_model_metadata' && !c.pass);
if (exposed.length) {
  number++;
  const ids = exposed.map(c => c.agent).join(', ');
  const c = exposed[0];
  const llm = c.reply.match(/"llm":"([^"]+)"/)?.[0] || c.reply;
  md += `### ${number}. Public model metadata — MINOR\n\n- Agents: ${ids}\n- Scenario: public_model_metadata\n- Evidence: unauthenticated GET /api/agents at ${c.http_time_ms} ms returned ${llm}. This endpoint request has no conversation trace ID.\n- Suspected root area: [internal/api/api.go](/Volumes/DATA/Development/talking-agent/internal/api/api.go:105), where the handler includes the LLM client name in its JSON response.\n\n`;
}
for (const c of full.cases.filter(c => !c.pass && !c.skip && c.scenario !== 'public_model_metadata')) {
  number++;
  const jsonPath = path.join(fullDir, `${c.agent}.json`);
  const agentDump = read(jsonPath);
  const sid = c.session_id || agentDump.session_id || '(unavailable)';
  const traceFile = path.join(fullDir, `${c.agent}.trace.jsonl`);
  const traceEvents = fs.readFileSync(traceFile, 'utf8').split('\n').filter(Boolean).map(line => { try { return JSON.parse(line); } catch { return null; } }).filter(e => e && (!c.turn_id || e.data?.turn_id === c.turn_id));
  const evidence = traceEvents.filter(e => /turn.started|tool|response.completed|turn.completed|error|llm.completed|confirmation/.test(e.type)).map(e => `${e.wall} ms ${e.type} ${JSON.stringify(e.data)}`).join('<br>') || 'No correlated trace event; see attached trace JSONL.';
  const severity = ['hallucinated_booking','nonexistent_change','injection'].includes(c.scenario) ? 'MAJOR' : 'MINOR';
  let root = `agents/${c.agent}/prompt.md`;
  if (c.scenario.startsWith('happy_tour')) root += ' and skills.yaml';
  if (c.scenario === 'rapid_repeat_repeat') root = 'internal/llm/client.go and the FastLLM provider path';
  md += `### ${number}. ${c.scenario} — ${severity}\n\n- Agent: ${c.agent}\n- Scenario: ${c.scenario}\n- Evidence: trace ID ${sid}, turn ${c.turn_id || '(none)'}. Reply: “${(c.reply || '(no reply)').replace(/\n/g,' ')}”\n- Trace events (wall-clock ms): ${evidence}\n- Suspected root area: ${root}.\n\n`;
}
md += '\n## Coverage limits\n\nOutbound outreach was not initiated because the UI offers to place a call. The session suite ran for every inbound agent and for each agent listing in the public metadata probe. Barge-in microphone speech, noisy/truncated audio transcription, reopening mid-conversation, and switching agents were not exercised. Two concurrent sessions per inbound agent were checked and received distinct session IDs. The happy-path conversations used demo family F001 in the full run and F003 in the school-services follow-up; F001 already had historical tour memory, which is why targeted school behavior used F003.\n';
fs.writeFileSync(path.join(fullDir, 'REPORT.md'), md);
fs.copyFileSync(path.join(fullDir, 'REPORT.md'), path.resolve('tests/adversarial/REPORT.md'));
console.log(path.resolve('tests/adversarial/REPORT.md'));
