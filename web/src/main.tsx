import React, { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

type RecordItem = { id: string; name: string; description?: string };
type Tool = { name: string; description: string; mutation: boolean };
type Agent = { config: { id: string; name: string; organization: string; role: string; industry: string; persona: Record<string, string>; memory: { namespace: string }; skills: string[]; knowledge: string[]; branding: { color: string } }; users: RecordItem[]; skills: Record<string, { description: string; instructions: string; tools: Tool[] }>; llm: string; memory: string };
type Event = { id: number; type: string; turn_id?: string; time: string; data: { text?: string; message?: string; tool?: string; duration_ms?: number; ttft_ms?: number; count?: number; usage?: { total_tokens: number }; memories?: { text: string }[]; [key: string]: unknown } };
type Chat = { id: string; role: 'user' | 'assistant'; text: string };
type Pending = { id: string; tool: string; arguments: Record<string, string>; expires: string };
async function api<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch('/api' + path, body === undefined ? undefined : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `Request failed (${response.status})`);
  return data;
}
const examples: Record<string, string[]> = {
  telecom: ['My internet upstairs is terrible again.', 'Explain my bill.', 'Compare available plans.', 'Optimize my Wi-Fi channel.', 'Show technician availability.'],
  hospital: ['I’d like to see a dermatologist this week.', 'Mornings normally work better for me.', 'Can I make another appointment with Dr. Ahmed?', 'Show my appointments.', 'Is DemoCare Plus insurance supported?', 'Where is visitor parking?'],
};
function App() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentID, setAgentID] = useState('');
  const [userID, setUserID] = useState('');
  const [session, setSession] = useState('');
  const [chat, setChat] = useState<Chat[]>([]);
  const [events, setEvents] = useState<Event[]>([]);
  const [pending, setPending] = useState<Pending[]>([]);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [starting, setStarting] = useState(false);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState('');
  const source = useRef<EventSource | null>(null);
  const lastID = useRef(0);
  const bottom = useRef<HTMLDivElement>(null);
  const agent = agents.find(a => a.config.id === agentID);
  useEffect(() => {
    let live = true;
    api<Agent[]>('/agents').then(items => { if (!live) return; setAgents(items); const first = items.find(a => a.config.industry === 'telecom') || items[0]; if (first) { setAgentID(first.config.id); setUserID(first.users[0]?.id || ''); } }).catch(e => { if (live) setError(e.message); });
    return () => { live = false; source.current?.close(); };
  }, []);
  useEffect(() => { bottom.current?.scrollIntoView({ behavior: 'smooth', block: 'end' }); }, [chat]);
  function reset() { source.current?.close(); source.current = null; setSession(''); setConnected(false); setChat([]); setEvents([]); setPending([]); setError(''); lastID.current = 0; }
  function changeAgent(id: string) { reset(); setAgentID(id); setUserID(agents.find(a => a.config.id === id)?.users[0]?.id || ''); }
  async function start() {
    reset(); setStarting(true);
    try {
      const data = await api<{ id: string }>('/sessions', { agent_id: agentID, user_id: userID });
      setSession(data.id);
      const stream = new EventSource(`/api/sessions/${data.id}/events`); source.current = stream;
      stream.onopen = () => setConnected(true);
      stream.onerror = () => setConnected(false);
      stream.onmessage = e => {
        if (source.current !== stream) return;
        const event = JSON.parse(e.data) as Event;
        if (event.id <= lastID.current) return;
        lastID.current = event.id;
        setEvents(old => [...old, event].slice(-300));
        if (event.type === 'agent.response.delta') {
          setChat(old => {
            const id = event.turn_id || 'assistant';
            const existing = old.find(m => m.id === id);
            if (existing) return old.map(m => m.id === id ? { ...m, text: m.text + (event.data.text || '') } : m);
            return [...old, { id, role: 'assistant', text: event.data.text || '' }];
          });
        }
        if (event.type === 'action.confirmation.required') setPending(old => [...old, event.data as unknown as Pending]);
        if (event.type === 'agent.error') setError(event.data.message || 'Agent error');
        if (event.type === 'turn.completed') setBusy(false);
      };
    } catch (e) { setError((e as Error).message); } finally { setStarting(false); }
  }
  async function send(text: string, confirmation?: string, reject = false) {
    if (!session || busy) return;
    setError(''); setBusy(true);
    if (!confirmation) setPending([]);
    else setPending(old => old.filter(p => p.id !== confirmation));
    setChat(old => [...old, { id: crypto.randomUUID(), role: 'user', text: confirmation ? (reject ? 'Cancel proposed action.' : 'Confirm proposed action.') : text }]);
    setMessage('');
    try { await api(`/sessions/${session}/messages`, confirmation ? { confirmation, reject } : { message: text }); }
    catch (e) { setError((e as Error).message); setBusy(false); }
  }
  async function cancel() { try { await api(`/sessions/${session}/cancel`, {}); } catch (e) { setError((e as Error).message); } }
  const memories = [...events].reverse().find(e => e.type === 'memory.retrieval.completed');
  const firstToken = [...events].reverse().find(e => e.type === 'llm.first_token');
  const tokens = events.reduce((n, e) => n + (e.data.usage?.total_tokens || 0), 0);
  const calls = events.filter(e => e.type === 'tool.completed' || e.type === 'tool.failed');
  const displayEvents = events.filter(e => e.type !== 'agent.response.delta').slice(-60).reverse();
  return <div className="app" style={{ '--accent': agent?.config.branding.color || '#2764d8' } as React.CSSProperties}>
    <header><div className="brand-icon">N</div><div><div className="eyebrow">SHARED PLATFORM · PHASE 1</div><h1>Enterprise AI Demo</h1></div><span className="header-tag">TEXT CONSOLE</span></header>
    <section className="controls" aria-label="Session setup">
      <label>Agent<select aria-label="Agent" value={agentID} disabled={busy || starting} onChange={e => changeAgent(e.target.value)}>{agents.map(a => <option key={a.config.id} value={a.config.id}>{a.config.industry === 'telecom' ? 'Telecom Support' : a.config.industry === 'hospital' ? 'Hospital Patient Services' : a.config.role}</option>)}</select></label>
      <label>Demo identity<select aria-label="Demo identity" value={userID} disabled={busy || starting} onChange={e => { reset(); setUserID(e.target.value); }}>{agent?.users.map(u => <option key={u.id} value={u.id}>{u.id} · {u.name} — {u.description}</option>)}</select></label>
      <button className="primary" disabled={!agent || !userID || busy || starting} onClick={start}>{starting ? 'Starting…' : session ? 'New session' : 'Start session'}</button>
    </section>
    {agent && <section className="agent-info"><div><strong>{agent.config.name}</strong><span> / {agent.config.organization}</span><p>{agent.config.role} · {Object.values(agent.config.persona).join(' · ')}</p></div><div className="modes"><span>{agent.llm}</span><span>Memory: {agent.memory}</span></div><details><summary>Configuration & capabilities</summary><div className="config-grid"><div><b>Memory namespace</b><p>{agent.config.memory.namespace}</p><b>Knowledge</b>{agent.config.knowledge.map(k => <p key={k}>{k}</p>)}</div><div><b>Skills & tools</b>{Object.entries(agent.skills).map(([id, s]) => <p key={id}><strong>{id}</strong> — {s.tools.map(t => t.name).join(', ')}</p>)}</div></div></details></section>}
    {error && <div className="error" role="alert">{error}<button onClick={() => setError('')} aria-label="Dismiss error">×</button></div>}
    <main>
      <section className="conversation"><div className="panel-title"><h2>Conversation</h2><span className={connected ? 'status connected' : 'status'}>{session ? connected ? 'Connected' : 'Reconnecting…' : 'No active session'}</span></div>
        <div className="messages" role="log" aria-label="Conversation messages" aria-live="polite">
          {!chat.length && <div className="empty"><div className="empty-icon">↗</div><h3>One AI platform. Many industries.</h3><p>Select an agent and a demo identity, then start a session. Tools and memories appear alongside the conversation.</p><small>All customer and patient records are fictional.</small></div>}
          {chat.map(m => <article key={m.id} className={`bubble ${m.role}`}><div className="speaker">{m.role === 'user' ? 'You' : agent?.config.name}</div><div>{m.text}</div></article>)}
          {busy && <div className="working" role="status">Agent is working…</div>}<div ref={bottom}/>
        </div>
        <div className="suggestions">{(examples[agent?.config.industry || ''] || []).map(text => <button key={text} disabled={!session || busy} onClick={() => setMessage(text)}>{text}</button>)}</div>
        <form onSubmit={e => { e.preventDefault(); if (message.trim()) void send(message.trim()); }}><label className="sr-only" htmlFor="message">Message</label><textarea id="message" placeholder={session ? 'Type a message…' : 'Start a session to begin'} disabled={!session || busy} value={message} maxLength={8000} onChange={e => setMessage(e.target.value)} onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); if (message.trim() && !busy) void send(message.trim()); } }}/>{busy ? <button type="button" onClick={cancel}>Stop</button> : <button className="primary" disabled={!session || !message.trim()} type="submit">Send ↗</button>}</form>
      </section>
      <aside><div className="panel-title"><h2>Agent activity</h2><span className="quiet">LIVE TRACE</span></div>
        <div className="activity-content">
          {pending.map(p => <div className="confirmation" key={p.id}><strong>Confirm change</strong><p>{p.tool}</p><pre>{JSON.stringify(p.arguments, null, 2)}</pre><small>Expires {new Date(p.expires).toLocaleTimeString()}</small><div><button className="primary" disabled={busy} onClick={() => send('', p.id)}>Confirm</button><button disabled={busy} onClick={() => send('', p.id, true)}>Cancel</button></div></div>)}
          <section><h3>Memory</h3><p className="metric">{memories?.data.count ?? 0}<small> relevant memories retrieved</small></p>{memories?.data.memories?.map((m, i) => <p className="memory" key={i}>{m.text}</p>)}<p className="quiet">Namespace: {agent?.config.memory.namespace || '—'}</p></section>
          <section><h3>Tools <span className="count">{calls.length}</span></h3>{calls.length ? calls.slice(-8).map(e => <div className="tool-row" key={e.id}><code>{e.data.tool}</code><span className={e.type === 'tool.failed' ? 'failed' : ''}>{e.type === 'tool.failed' ? 'failed · ' : ''}{e.data.duration_ms} ms</span></div>) : <p className="quiet">Tool calls will appear here.</p>}</section>
          <section><h3>LLM</h3><div className="metrics"><div><b>{firstToken?.data.ttft_ms ?? '—'}<small> ms</small></b><span>Latest text TTFT</span></div><div><b>{tokens || '—'}</b><span>Reported tokens</span></div></div></section>
          <section><h3>Event stream</h3><div className="trace">{displayEvents.map(e => <details key={e.id}><summary><span>{e.type}</span><time>{new Date(e.time).toLocaleTimeString()}</time></summary><pre>{JSON.stringify(e.data, null, 2)}</pre></details>)}</div></section>
        </div>
      </aside>
    </main>
    <footer>Same runtime · Configurable skills · HTTP tools · Scoped memory <span>Future transports connect to the same agent API</span></footer>
  </div>;
}
createRoot(document.getElementById('root')!).render(<React.StrictMode><App/></React.StrictMode>);
