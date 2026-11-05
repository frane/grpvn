export type Profile = { id: string; name: string; kind: 'human'; admin: boolean };
export type Session = { authenticated: boolean; profile?: Profile; local: boolean };
export type Channel = { name: string; label: string; source: string; source_id?:string;account_name?:string; unread: number; write: boolean; open: boolean };
export type Person = { id: string; name: string; kind: 'human' | 'agent'; status: string };
export type Message = { seq: number; id: string; sender: string; target: string; body: string; chain_root: string; parent_id: string | null; created_at: number; kind: string; mine: boolean; pending: boolean };
export type Approval = { id: string; name: string; code: string; channels: string[] };
export class ApiError extends Error { constructor(public status: number, message: string) { super(message); } }
const routes: Record<string,string> = {
 session:'/v1/auth/session',login:'/v1/auth/login',signup:'/v1/accounts',logout:'/v1/auth/logout',bootstrap:'/v1/auth/local-session',
 channels:'/v1/chat/channels',messages:'/v1/chat/messages',read:'/v1/chat/read-markers',create:'/v1/chat/channels',invite:'/v1/chat/invitations',join:'/v1/chat/memberships',people:'/v1/chat/participants',approvals:'/v1/access-requests',sources:'/v1/workspaces','connect/status':'/v1/workspace-connections'
};
export async function api<T>(path: string, body?: unknown): Promise<T> {
  const [name,query] = path.split('?');
  const response = await fetch((name.startsWith('connections/') ? '/v1/workspace-connections/'+name.slice(12) : name==='sources'&&body!==undefined ? '/v1/workspace-connections' : name==='approvals'&&body!==undefined ? '/v1/access-approvals' : routes[name]) + (query ? '?' + query : ''), { credentials: 'same-origin', ...(body !== undefined ? { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}) });
  const data = await response.json();
  if (!response.ok) throw new ApiError(response.status, data.error || 'Something went wrong. Try again.');
  return data;
}
export function initials(name: string) { return name.split(/[-_ .]/).filter(Boolean).slice(0, 2).map(s => s[0].toUpperCase()).join(''); }
export function color(name: string) { let n = 0; for (const c of name) n = (n * 31 + c.charCodeAt(0)) | 0; return ['sage', 'blue', 'amber', 'violet', 'rose'][Math.abs(n) % 5]; }
export function shortName(name: string) { return name.replace(/~.*/, ''); }
