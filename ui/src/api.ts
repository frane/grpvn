export type Profile = { id: string; name: string; kind: 'human'; admin: boolean };
export type Session = { authenticated: boolean; profile?: Profile; local: boolean };
export type Channel = { name: string; label: string; source: string; source_id?:string;account_name?:string; unread: number; write: boolean; open: boolean; joined?:boolean; archived?:boolean;manage?:boolean };
export type Member = Person & {grants:{channel:string;write:boolean}[];admin:boolean;local?:boolean;self?:boolean};
export type UploadedFile={id:string;name:string;type:string;size:number;image:boolean;url:string};
export type Person = { id: string; name: string; kind: 'human' | 'agent'; status: string;runtime?:string;last_seen?:number;last_message?:number };
export type Message = { seq: number; id: string; sender: string; target: string; body: string; chain_root: string; parent_id: string | null; created_at: number; kind: string; mine: boolean; pending: boolean };
export type Approval = { id: string; name: string; code: string; channels: string[] };
export class ApiError extends Error { constructor(public status: number, message: string) { super(message); } }
const routes: Record<string,string> = {
 session:'/v1/auth/session',login:'/v1/auth/login',signup:'/v1/accounts',logout:'/v1/auth/logout',bootstrap:'/v1/auth/local-session',
 channels:'/v1/chat/channels',messages:'/v1/chat/messages',read:'/v1/chat/read-markers',create:'/v1/chat/channels',invite:'/v1/chat/invitations',join:'/v1/chat/memberships',memberships:'/v1/chat/memberships',members:'/v1/members',people:'/v1/chat/participants',approvals:'/v1/access-requests',sources:'/v1/workspaces','connect/status':'/v1/workspace-connections'
};
export async function api<T>(path: string, body?: unknown, method?:string): Promise<T> {
  const [name,query] = path.split('?');
  const endpoint=name.startsWith('members/')?'/v1/members/'+name.slice(8):name.startsWith('connections/') ? '/v1/workspace-connections/'+name.slice(12) : name==='sources'&&body!==undefined ? '/v1/workspace-connections' : name==='approvals'&&body!==undefined ? '/v1/access-approvals' : routes[name];
  const response = await fetch(endpoint + (query ? '?' + query : ''), { credentials: 'same-origin',method:method||(body===undefined?'GET':'POST'), ...(body !== undefined ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}) });
  const data = await response.json();
  if (!response.ok) throw new ApiError(response.status, data.error || 'Something went wrong. Try again.');
  return data;
}
export function fileURL(id:string,target:string){return '/v1/files/'+id+'?target='+encodeURIComponent(target);}
export async function uploadFile(file:File,target:string):Promise<UploadedFile>{
 const body=new FormData();body.set('target',target);body.set('file',file);
 const response=await fetch('/v1/files',{method:'POST',body,credentials:'same-origin'});const result=await response.json();if(!response.ok)throw new ApiError(response.status,result.error||'Upload failed');return result;
}
export function initials(name: string) { return name.split(/[-_ .]/).filter(Boolean).slice(0, 2).map(s => s[0].toUpperCase()).join(''); }
export function color(name: string) { let n = 0; for (const c of name) n = (n * 31 + c.charCodeAt(0)) | 0; return ['sage', 'blue', 'amber', 'violet', 'rose'][Math.abs(n) % 5]; }
export function shortName(name: string) { return name.replace(/~.*/, ''); }
