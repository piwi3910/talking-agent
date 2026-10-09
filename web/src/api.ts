// Shared API types and helpers for the Stage console.
export type RecordItem = { id: string; name: string; description?: string };
export type Tool = { name: string; description: string; mutation: boolean };
export type Agent = {
  config: {
    id: string;
    name: string;
    organization: string;
    role: string;
    industry: string;
    persona: Record<string, string>;
    memory: { namespace: string; domain?: string };
    skills: string[];
    knowledge: string[];
    branding: Record<string, string>;
  };
  users: RecordItem[];
  skills: Record<
    string,
    { description: string; instructions: string; tools: Tool[] }
  >;
  llm: string;
  memory: string;
};
export type Event = {
  id: number;
  type: string;
  turn_id?: string;
  time: string;
  data: {
    text?: string;
    message?: string;
    tool?: string;
    duration_ms?: number;
    ttft_ms?: number;
    count?: number;
    usage?: { total_tokens: number };
    memories?: { text: string }[];
    [key: string]: unknown;
  };
};
export type Chat = { id: string; role: "user" | "assistant"; text: string };
export type Pending = {
  id: string;
  tool: string;
  arguments: Record<string, string>;
  expires: string;
};
export type SpeechInfo = { stt?: string; tts?: string };

export async function api<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(
    "/api" + path,
    body === undefined
      ? undefined
      : {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
  );
  // Some endpoints (e.g. session open) answer 202 with an empty body.
  const raw = await response.text();
  let data: { error?: string } = {};
  try {
    data = raw ? JSON.parse(raw) : {};
  } catch {
    if (response.ok) data = {};
    else throw new Error(`Request failed (${response.status})`);
  }
  if (!response.ok)
    throw new Error(data.error || `Request failed (${response.status})`);
  return data as T;
}

const industryLabels: Record<string, string> = {
  school: "Demo family",
  aquila: "Family / contact",
};
export const industryLabel = (industry?: string) =>
  (industry && industryLabels[industry]) || "Demo customer";

export function actionLabel(tool: string) {
  const labels: Record<string, string> = {
    "wifi.optimize": "Update your Wi-Fi settings?",
    "wifi.restart": "Restart your router?",
    "plan.change": "Change your plan?",
    "appointment.book": "Book this appointment?",
    "appointment.reschedule": "Reschedule your appointment?",
    "appointment.cancel": "Cancel your appointment?",
    "technician.book": "Book a technician visit?",
    "ticket.create": "Create a support ticket?",
    "ticket.update": "Update your support ticket?",
    "support.create_request": "Send your service request?",
  };
  return labels[tool] || "Confirm this change?";
}

// Stage accents: bright on the dark stage, the persona's own brand colour in light mode.
const stageAccents: Record<string, string> = {
  telecom: "#6E9BFF",
  school: "#B3A1FF",
  aquila: "#7FB0F5",
};
export function accentOf(agent?: Agent): { dark: string; light: string } {
  const brand = agent?.config.branding.color || "#2764d8";
  return {
    dark: stageAccents[agent?.config.industry || ""] || "#6E9BFF",
    light: brand,
  };
}
export function markOf(agent?: Agent) {
  return agent?.config.branding.mark || agent?.config.name.slice(0, 1) || "A";
}

// Short capability tags for a persona card, derived from its configuration.
export function tagsOf(agent: Agent): string[] {
  const tags: string[] = [];
  const opening = agent.config.persona?.opening;
  if (opening === "outbound") tags.push("Outbound");
  if (opening === "inbound") tags.push("Speaks first");
  const tools = Object.values(agent.skills || {}).flatMap((s) => s.tools);
  if (tools.some((t) => t.mutation)) tags.push("Confirmations");
  if (tools.length) tags.push(`${tools.length} tools`);
  if (agent.config.knowledge?.length) tags.push("Knowledge");
  if (agent.config.memory?.domain) tags.push("Shared memory");
  else tags.push("Memory");
  return tags.slice(0, 4);
}

export type EventKind =
  | "STT"
  | "MEMORY"
  | "SKILL"
  | "TOOL"
  | "LLM"
  | "APPROVAL"
  | "TTS"
  | "TURN"
  | "ERROR"
  | "EVENT";
export function kindOf(type: string): EventKind {
  if (type.startsWith("stt.")) return "STT";
  if (type.startsWith("tts.")) return "TTS";
  if (type.startsWith("memory.")) return "MEMORY";
  if (type.startsWith("skill.")) return "SKILL";
  if (type.startsWith("tool.")) return "TOOL";
  if (type.startsWith("llm.")) return "LLM";
  if (type.startsWith("action.")) return "APPROVAL";
  if (type.startsWith("turn.")) return "TURN";
  if (type.includes("error") || type.includes("failed")) return "ERROR";
  return "EVENT";
}

// One-line human detail for an event row in the X-ray.
export function detailOf(e: Event): string {
  const d = e.data;
  if (d.tool)
    return `${d.tool}${typeof d.duration_ms === "number" ? ` · ${d.duration_ms} ms` : ""}`;
  if (typeof d.text === "string" && d.text) return `“${d.text.slice(0, 120)}”`;
  if (typeof d.message === "string" && d.message) return d.message;
  if (typeof d.count === "number") return `${d.count} recalled`;
  if (typeof d.ttft_ms === "number") return `${d.ttft_ms} ms to first token`;
  for (const key of ["ttfa_ms", "finalization_ms", "duration_ms"]) {
    const v = d[key];
    if (typeof v === "number")
      return `${key.replace("_ms", "")} ${Math.round(v)} ms`;
  }
  return "";
}

export function clock(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
}
