import React, { useCallback, useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { ArrowDown, ArrowLeft, ArrowUp, Bell, Bot, Check, CheckCheck, ChevronDown, ChevronRight, Copy, Globe, Hash, Link, LockKeyhole, LogOut, Menu, MessageCircle, MessageSquare, MoreHorizontal, PanelRightClose, Plus, Search, Settings2, ShieldCheck, Sparkles, Users, WifiOff, X } from 'lucide-react';
import { api, ApiError, Approval, Channel, color, initials, Message, Person, Session, shortName } from './api';
import './style.css';
import {openExternal} from './platform';

function Avatar({ name, human = false, small = false }: { name: string; human?: boolean; small?: boolean }) {
  return <span className={`avatar ${color(name)} ${small ? 'small' : ''}`} aria-hidden="true">{human ? initials(name) : <Bot size={small ? 14 : 19} strokeWidth={1.7} />}</span>;
}
function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [people, setPeople] = useState<Person[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [target, setTarget] = useState(localStorage.getItem('grpvn.target') || '#general');
  const [thread, setThread] = useState<Message | null>(null);
  const [tab, setTab] = useState<'chats' | 'people'>('chats');
  const [search, setSearch] = useState('');
  const [error, setError] = useState('');
  const [connected, setConnected] = useState(false);
  const [modal, setModal] = useState<'create' | 'invite' | 'join' | 'connect' | 'approvals' | null>(null);
  const closeModal = useCallback(()=>setModal(null),[]);
  const [sidebar, setSidebar] = useState(false);
  const [localChat,setLocalChat]=useState(false);
  const [loading, setLoading] = useState(false);
  const [more, setMore] = useState(false);
  const scroll = useRef<HTMLDivElement>(null);
  const bottom = useRef(true);
  const currentTarget = useRef(target);
  currentTarget.current = target;
  const selected = channels.find(c => c.name === target);
  const profile = session?.profile;
  const activeName=selected?.account_name || profile?.name || "";
  const dm = target.startsWith('@');
  const label = dm ? target.slice(1) : (selected?.label || target).replace(/^#/, '');
  const [toast, setToast] = useState('');
  useEffect(() => { if (toast) { const t = setTimeout(() => setToast(''), 3500); return () => clearTimeout(t); } }, [toast]);
  const fail = useCallback((e: unknown) => { if (e instanceof ApiError && e.status === 401) setSession({ authenticated: false, local: false }); setError(e instanceof Error ? e.message : 'Could not connect'); }, []);
  const refreshSession = useCallback(async () => { try { const state=await api<Session>('session');setSession(state);const next=new URLSearchParams(location.search).get('next');if(state.authenticated&&next){const where=new URL(next,location.origin);if(where.origin===location.origin&&where.pathname==='/oauth/authorize'&&where.searchParams.get('actor')==='human'){location.replace(where.pathname+where.search);}} } catch (e) { fail(e); } }, [fail]);
  useEffect(() => {
    const ticket = new URLSearchParams(location.hash.slice(1)).get('ticket');
    if (ticket) { history.replaceState(null, '', '/'); api('bootstrap', { Ticket: ticket }).then(refreshSession).catch(e => { fail(e); refreshSession(); }); }
    else refreshSession();
  }, [refreshSession, fail]);
  const refreshLists = useCallback(async () => {
    const [c, p] = await Promise.all([api<{ channels: Channel[] }>('channels'), api<{ people: Person[] }>('people')]);
    setChannels(c.channels); setPeople(p.people);
    if (c.channels.length && !currentTarget.current.startsWith('@') && !c.channels.some(x => x.name === currentTarget.current)) setTarget(c.channels[0].name);
  }, []);
  const refreshMessages = useCallback(async (where: string) => {
    const page = await api<{ messages: Message[]; more: boolean }>('messages?target=' + encodeURIComponent(where));
    if (currentTarget.current !== where) return;
    setMessages(page.messages); setMore(page.more);
    if (page.messages.length && document.visibilityState === 'visible' && document.hasFocus()) api('read', { Target: where, Position: Math.max(...page.messages.map(m => m.seq)) }).then(()=>setChannels(prev=>prev.map(c=>c.name===where?{...c,unread:0}:c))).catch(() => {});
  }, []);
  useEffect(() => {
    if (!session?.authenticated) return;
    refreshLists().catch(fail);
    const events = new EventSource('/v1/chat/events');
    events.addEventListener('connected', () => { setConnected(true); refreshLists().catch(fail); refreshMessages(currentTarget.current).catch(fail); });
    events.addEventListener('changed', () => { refreshLists().catch(fail); refreshMessages(currentTarget.current).catch(fail); });
    events.addEventListener('signed-out', () => { events.close(); refreshSession(); });
    events.onerror = () => setConnected(false);
    return () => events.close();
  }, [session?.authenticated, refreshLists, refreshMessages, refreshSession, fail]);
  useEffect(() => {
    if (!session?.authenticated) return;
    setThread(null); setError(''); setMessages([]); setLoading(true); bottom.current = true;
    localStorage.setItem('grpvn.target', target);
    refreshMessages(target).catch(fail).finally(() => setLoading(false));
  }, [target, session?.authenticated, refreshMessages, fail]);
  useEffect(()=>{if(!session?.authenticated)return;const focused=()=>{if(document.visibilityState==='visible'){refreshLists().catch(fail);refreshMessages(currentTarget.current).catch(fail);};};window.addEventListener('focus',focused);document.addEventListener('visibilitychange',focused);return()=>{window.removeEventListener('focus',focused);document.removeEventListener('visibilitychange',focused);};},[session?.authenticated,refreshLists,refreshMessages,fail]);
  useEffect(() => { if (bottom.current) scroll.current?.scrollTo({ top: scroll.current.scrollHeight, behavior: 'instant' }); }, [messages]);
  async function send(body: string, reply?: Message, key?: string) {
    const result = await api<{ pending: boolean }>('messages', { Target: reply?.id || target, Body: body, IdempotencyKey: key || crypto.randomUUID() });
    await refreshMessages(target); await refreshLists(); setError(''); bottom.current = true;
    if (result.pending) setToast('Saved locally. Delivery will resume when the source is available.');
  }
  async function older() {
    if (!messages.length) return;
    try { const page = await api<{ messages: Message[]; more: boolean }>(`messages?target=${encodeURIComponent(target)}&before=${messages[0].seq}`); setMessages(prev => [...page.messages, ...prev]); setMore(page.more); } catch (e) { fail(e); }
  }
  function select(where: string) { setTarget(where); setTab('chats'); setSidebar(false); }
  const filtered = messages.filter(m => !search || `${m.body} ${m.sender}`.toLowerCase().includes(search.toLowerCase()));
  const roots = filtered.filter(m => !m.parent_id || !messages.some(p => p.id === m.chain_root));
  const replies = thread ? messages.filter(m => m.chain_root === thread.chain_root && m.id !== thread.chain_root) : [];
  const sources = [...new Set(channels.map(c => c.source))];
  if (!session) return <div className="startup"><Logo /><span>Opening your conversations…</span>{error && <p role="alert">{error}</p>}</div>;
  if (!session.authenticated) return <SignIn onDone={refreshSession} initialError={error} />;
  return <div className="app">
    <aside className={`sidebar ${sidebar ? 'visible' : ''}`}>
      <div className="workspace-head"><Logo /><span className="workspace-title">grpvn<span>{session.local ? 'On this computer' : 'Workspace'}</span></span><button className="icon subtle" aria-label="Workspace actions" onClick={() => setModal(session.local ? 'connect' : 'join')}><ChevronDown size={16} /></button></div>
      <div className="side-search"><Search size={15} /><input aria-label="Find a conversation" placeholder="Find a conversation" value={search} onChange={e => setSearch(e.target.value)} /><kbd>⌕</kbd></div>
      <nav className="main-nav"><button className={tab === 'chats' ? 'active' : ''} onClick={() => setTab('chats')}><MessageSquare size={17} />Conversations<span>{channels.reduce((a, c) => a + c.unread, 0) || ''}</span></button><button className={tab === 'people' ? 'active' : ''} onClick={() => setTab('people')}><Users size={17} />People & agents<span>{people.length}</span></button></nav>
      <div className="channel-scroll">
        {sources.map(source => <section key={source} className="channel-section"><div className="section-head"><span>{source === 'workspace' ? 'Channels' : source === 'local' ? 'Local channels' : source}</span><button className="icon subtle" aria-label="Create channel" onClick={() => setModal('create')}><Plus size={15} /></button></div>{channels.filter(c => c.source === source && (!search || c.name.toLowerCase().includes(search.toLowerCase()))).map(c => <button key={c.name} className={`channel ${target === c.name && tab === 'chats' ? 'selected' : ''} ${c.unread ? 'unread' : ''}`} onClick={() => select(c.name)}>{c.open ? <Hash size={17} /> : <LockKeyhole size={15} />}<span>{c.label.replace(/^#/, '')}</span>{c.unread > 0 && <b>{c.unread > 99 ? '99+' : c.unread}</b>}</button>)}</section>)}
        {!channels.length && <section className="channel-section"><div className="section-head"><span>Channels</span></div><button className="channel add" onClick={() => setModal('create')}><Plus size={16} />Create channel</button></section>}
        <section className="channel-section"><div className="section-head"><span>Direct messages</span><button className="icon subtle" aria-label="Find someone to message" onClick={() => setTab('people')}><Plus size={15} /></button></div>{people.filter(p => p.status === 'active' && p.id !== profile?.id).slice(0, 12).map(p => <button key={p.id} className={`channel person-channel ${target === '@' + p.name ? 'selected' : ''}`} onClick={() => select('@' + p.name)}><Avatar name={p.name} human={p.kind === 'human'} small /><span>{shortName(p.name)}</span>{p.kind === 'agent' && <span className="agent-dot" />}</button>)}</section>
      </div>
      <div className="sidebar-footer">{session.local && <button className="channel add" onClick={()=>setModal('connect')}><Plus size={16}/>Add workspace</button>}<button className="channel add" onClick={() => setModal('invite')}><Plus size={16} />Invite collaborator</button>{profile?.admin && <button className="channel add" onClick={() => setModal('approvals')}><ShieldCheck size={16} />Connection requests</button>}<div className="profile"><Avatar name={activeName} human /><div><strong>{activeName}</strong><span><i className={connected ? 'online' : ''} />{connected ? 'Connected' : 'Reconnecting…'}</span></div><button className="icon subtle" aria-label="Sign out" title="Sign out" onClick={async () => { await api('logout', {}); refreshSession(); }}><LogOut size={16} /></button></div></div>
    </aside>
    <main className="main">
      {session.local && !channels.length && !localChat ? <div className="workspace-setup"><WorkspaceConnect onDone={async(where)=>{await refreshLists();if(where)select(where);setLocalChat(true);}}/><button className="text-button" onClick={()=>{setLocalChat(true);setModal('create');}}>Use local chat</button></div> : tab === 'people' ? <><header className="toolbar"><button className="icon mobile-only" aria-label="Open sidebar" onClick={() => setSidebar(true)}><Menu size={19} /></button><Users size={20} /><h1>People & agents</h1><span className="toolbar-detail">Everyone in your workspace</span><button className="button secondary" onClick={() => setModal('invite')}><Plus size={15} />Invite</button></header><div className="directory"><div className="page-eyebrow">Workspace</div><h2>People & agents</h2><p>Select a person or agent to send a direct message.</p><div className="people-grid">{people.map(p => <button className="person-card" key={p.id} onClick={() => select('@' + p.name)}><Avatar name={p.name} human={p.kind === 'human'} /><div><strong>{p.name}</strong><span>{p.kind === 'human' ? 'Human' : 'AI agent'} · {p.status === 'active' ? 'Workspace member' : 'Awaiting invitation'}</span></div><MessageCircle size={17} /></button>)}</div></div></> : <>
      <header className="toolbar"><button className="icon mobile-only" aria-label="Open sidebar" onClick={() => setSidebar(true)}><Menu size={19} /></button>{dm ? <MessageCircle size={20} /> : selected?.open ? <Hash size={20} /> : <LockKeyhole size={18} />}<h1>{label}</h1><span className="toolbar-detail">{dm ? 'Direct conversation' : selected?.open ? 'Open channel' : 'Private channel'}</span><div className="toolbar-actions"><button className="button secondary" onClick={() => setModal('invite')}><Users size={15} /><span>Invite</span></button><button className="icon" aria-label="Channel details" onClick={() => setToast(dm ? 'Only the participants can read this conversation.' : 'Private channels are visible to their members. Writers can invite collaborators.')}><MoreHorizontal size={19} /></button></div></header>
      {!connected && <div className="connection-banner"><WifiOff size={15} />Reconnecting. Your conversations are saved; live updates will resume automatically.</div>}
      {error && <div className="error-banner" role="alert">{error}<button className="icon" aria-label="Dismiss error" onClick={() => setError('')}><X size={15} /></button></div>}
      <div className="chat-layout"><section className="conversation"><div className="messages" ref={scroll} onScroll={e => { const el = e.currentTarget; bottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 100; }}>
        {!loading && !messages.length && <div className="channel-intro"><div className="channel-symbol">{dm ? <MessageCircle size={27} /> : <Hash size={28} strokeWidth={1.6} />}</div><h2>No messages yet</h2><p>{dm ? `Send a message to ${shortName(label)}.` : `Send the first message in #${label}.`}</p></div>}
        {more && <button className="load-older" onClick={older}>Load earlier messages</button>}
        {loading && <div className="loading-line">Loading conversation…</div>}
        {roots.map((m, i) => <React.Fragment key={m.id}>{(i === 0 || new Date(m.created_at).toDateString() !== new Date(roots[i - 1].created_at).toDateString()) && <div className="date-divider"><span>{day(m.created_at)}</span></div>}<MessageRow message={m} onReply={() => setThread(m)} replyCount={messages.filter(x => x.chain_root === m.id && x.parent_id).length} /></React.Fragment>)}
      </div><div className="composer-wrap"><Composer key={target} placeholder={dm ? `Message ${shortName(label)}` : `Message #${label}`} disabled={!!selected && !selected.write} onSend={(body, key) => send(body, undefined, key)} /><div className="composer-footnote"><span><strong>↵</strong> to send · <strong>⇧ ↵</strong> for a new line</span><span>Shared with {dm ? 'this participant' : 'channel members'}</span></div></div></section>
      {thread && <section className="thread"><header><div><MessageSquare size={17} /><strong>Thread</strong><span>#{label}</span></div><button className="icon" aria-label="Close thread" onClick={() => setThread(null)}><PanelRightClose size={18} /></button></header><div className="thread-messages"><MessageRow message={messages.find(m => m.id === thread.chain_root) || thread} /><div className="thread-divider">{replies.length} {replies.length === 1 ? 'reply' : 'replies'}</div>{replies.map(m => <MessageRow key={m.id} message={m} />)}</div><div className="thread-compose"><Composer key={thread.id} placeholder="Reply in thread…" onSend={(body, key) => send(body, thread, key)} /></div></section>}
      </div></>}
    </main>
    {modal && <Modal mode={modal} target={target} remote={session.local?selected?.source_id:undefined} channels={channels} onClose={closeModal} onDone={async (where?: string) => { await refreshLists(); if (where) select(where); setModal(null); }} toast={setToast} />}
    {toast && <div className="toast" role="status"><CheckCheck size={17} />{toast}</div>}
    {sidebar && <button className="sidebar-scrim" aria-label="Close sidebar" onClick={() => setSidebar(false)} />}
  </div>;
}
function Logo() { return <span className="logo" aria-label="grpvn"><MessageSquare size={22} strokeWidth={1.6} /><span className="logo-dot" /></span>; }
function day(time: number) { const d = new Date(time); const today = new Date(); return d.toDateString() === today.toDateString() ? 'Today' : new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'long', day: 'numeric' }).format(d); }
function MessageRow({ message: m, onReply, replyCount = 0 }: { message: Message; onReply?: () => void; replyCount?: number }) {
  return <article className={`message ${m.pending ? 'pending' : ''}`} data-message-id={m.id}><Avatar name={m.sender} human={m.kind === 'human'} /><div className="message-content"><div className="message-byline"><strong>{shortName(m.sender)}</strong>{m.kind !== 'human' && <span className="bot-badge">AGENT</span>}{m.mine && <span className="you-label">you</span>}<time dateTime={new Date(m.created_at).toISOString()} title={new Date(m.created_at).toLocaleString()}>{new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(m.created_at)}</time>{m.pending && <span className="queued">Waiting to sync</span>}</div><div className="message-body">{m.body}</div>{replyCount > 0 && <button className="replies" onClick={onReply}><MessageCircle size={14} />{replyCount} {replyCount === 1 ? 'reply' : 'replies'}<ChevronRight size={13} /></button>}</div>{onReply && <button className="message-action icon" aria-label={'Reply to ' + m.sender} title="Reply in thread" onClick={onReply}><MessageSquare size={16} /></button>}</article>;
}
function Composer({ placeholder, disabled, onSend }: { placeholder: string; disabled?: boolean; onSend: (body: string, key: string) => Promise<void> }) {
  const [body, setBody] = useState(''); const [sending, setSending] = useState(false); const [error, setError] = useState(''); const key = useRef(crypto.randomUUID()); const area = useRef<HTMLTextAreaElement>(null);
  async function submit() { if (!body.trim() || sending || disabled) return; setSending(true); setError(''); try { await onSend(body.trim(), key.current); setBody(''); key.current = crypto.randomUUID(); if (area.current) area.current.style.height = ''; } catch (e) { setError(e instanceof Error ? e.message : 'Could not send. Try again.'); } finally { setSending(false); area.current?.focus(); } }
  return <><div className={`composer ${disabled ? 'disabled' : ''}`}><textarea ref={area} aria-label={placeholder} placeholder={disabled ? 'You have read-only access to this channel' : placeholder} value={body} disabled={disabled || sending} rows={2} maxLength={65536} onChange={e => { setBody(e.target.value); key.current = crypto.randomUUID(); e.currentTarget.style.height = 'auto'; e.currentTarget.style.height = Math.min(e.currentTarget.scrollHeight, 200) + 'px'; }} onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); } }} /><div className="composer-bottom"><span className="composer-hint"><MessageCircle size={15} /><span></span></span><button className="send-button" aria-label="Send message" disabled={!body.trim() || disabled || sending} onClick={submit}><ArrowUp size={18} /></button></div></div>{error && <p className="form-error" role="alert">{error} Your draft is saved here; retry uses the same delivery key.</p>}</>;
}
function SignIn({ onDone, initialError }: { onDone: () => void; initialError: string }) {
  const [signup, setSignup] = useState(new URLSearchParams(location.search).has('invited') || new URLSearchParams(location.hash.slice(1)).has('invite'));
  const [name, setName] = useState(''); const [password, setPassword] = useState(''); const [invitation, setInvitation] = useState(new URLSearchParams(location.hash.slice(1)).get('invite') || ''); const [busy, setBusy] = useState(false); const [error, setError] = useState(initialError);
  async function submit(e: React.FormEvent) { e.preventDefault(); setBusy(true); setError(''); try { await api(signup ? 'signup' : 'login', { Name: name, Password: password, ...(signup ? { Invitation: invitation } : {}) }); if(!new URLSearchParams(location.search).has('next'))history.replaceState(null, '', '/'); onDone(); } catch (e) { setError(e instanceof Error ? e.message : 'Could not sign in'); } finally { setBusy(false); } }
  return <div className="auth"><div className="auth-brand"><Logo /><strong>grpvn</strong><span>Chat for people and agents</span></div><div className="auth-card"><span className="page-eyebrow">{signup ? 'You’re invited' : 'grpvn'}</span><h1>{signup ? 'Create account' : 'Sign in'}</h1><p>{signup ? 'Choose your name and password.' : 'Sign in to this workspace.'}</p><form onSubmit={submit}><label>Your handle<input name="username" autoComplete="username" autoFocus required value={name} onChange={e => setName(e.target.value)} placeholder="Your name" /></label><label>Password<input name="password" type="password" autoComplete={signup ? 'new-password' : 'current-password'} minLength={signup ? 12 : undefined} required value={password} onChange={e => setPassword(e.target.value)} placeholder={signup ? 'At least 12 characters' : 'Your password'} /></label>{signup && !new URLSearchParams(location.search).has('invited') && <label>Invitation link<input name="invitation" required value={invitation} onChange={e => setInvitation(e.target.value)} placeholder="Paste the invitation you received" /></label>}{error && <div className="form-error" role="alert">{error}</div>}<button className="button primary wide" disabled={busy}>{busy ? 'One moment…' : signup ? 'Join the workspace' : 'Sign in'}<ChevronRight size={16} /></button></form><div className="auth-switch">{signup ? 'Already have a profile?' : 'Have an invitation?'}<button onClick={() => { setSignup(!signup); setError(''); }}>{signup ? 'Sign in' : 'Join the workspace'}</button></div></div><a className="agent-guide" href="/llm.txt"><Bot size={16} />Connecting an agent? Read the guide<ChevronRight size={14} /></a><div className="auth-footer"></div></div>;
}
function WorkspaceConnect({onDone}:{onDone:(target?:string)=>Promise<void>}){
  const[address,setAddress]=useState('');const[busy,setBusy]=useState(false);const[error,setError]=useState('');
  const[attempt,setAttempt]=useState('');const[signInURL,setSignInURL]=useState('');const[workspace,setWorkspace]=useState('');
  const finish=useRef(onDone);finish.current=onDone;
  useEffect(()=>{if(!attempt)return;let stopped=false;const timer=setInterval(async()=>{try{const result=await api<{status:string;detail:string;target:string}>('connections/'+encodeURIComponent(attempt));if(stopped)return;if(result.status==='connected'){clearInterval(timer);await finish.current(result.target||undefined);}else if(result.status==='failed'){clearInterval(timer);setAttempt('');setError(result.detail||'Sign-in failed. Try again.');}}catch(e){if(!stopped){clearInterval(timer);setAttempt('');setError((e as Error).message);}}},1500);return()=>{stopped=true;clearInterval(timer);};},[attempt]);
  function openBrowser(address:string){openExternal(address);}
  async function submit(e:React.FormEvent){e.preventDefault();setBusy(true);setError('');try{const result=await api<{status:string;url:string;attempt:string;workspace:string;target?:string;opened?:boolean}>('sources',{URL:address});if(result.status==='connected'){await onDone(result.target||undefined);return;}setAttempt(result.attempt);setSignInURL(result.url);setWorkspace(result.workspace);openExternal(result.url);}catch(e){setError((e as Error).message);}finally{setBusy(false);}}
  return <><div className="modal-symbol"><Globe size={24}/></div><h2>{attempt?'Finish signing in':'Add workspace'}</h2>{attempt?<><p className="connect-description">Sign in to <strong>{new URL(workspace).host}</strong> in your browser. This window will update automatically.</p><button className="button primary wide" onClick={()=>openBrowser(signInURL)}>Open sign-in page</button><button className="text-button" onClick={()=>{setAttempt('');setError('');}}>Use a different address</button></>:<form onSubmit={submit}><p className="connect-description">Enter a workspace address or an invitation link.</p><label>Workspace address or invitation<input autoFocus required value={address} onChange={e=>setAddress(e.target.value)} placeholder="chat.example.com" autoComplete="url"/></label>{error&&<p className="form-error" role="alert">{error}</p>}<button className="button primary wide" disabled={busy}>{busy?'Connecting…':'Continue'}<ChevronRight size={16}/></button></form>}{attempt&&error&&<p className="form-error" role="alert">{error}</p>}</>;
}
function Modal({ mode, target, remote, channels, onClose, onDone, toast }: { remote?:string; mode: string; target: string; channels: Channel[]; onClose: () => void; onDone: (where?: string) => Promise<void>; toast: (s: string) => void }) {
  const [channel, setChannel] = useState(mode === 'create' ? '' : target.startsWith('#') ? target : channels[0]?.name || ''); const [open, setOpen] = useState(false); const [agent, setAgent] = useState(''); const [link, setLink] = useState(''); const [readOnly, setReadOnly] = useState(false); const [error, setError] = useState(''); const [busy, setBusy] = useState(false); const [requests, setRequests] = useState<Approval[]>([]);
  useEffect(() => { function key(e: KeyboardEvent) { if (e.key === 'Escape') onClose(); } document.addEventListener('keydown', key); const before = document.activeElement as HTMLElement; return () => { document.removeEventListener('keydown', key); before?.focus(); }; }, [onClose]);
  useEffect(() => { if (mode === 'approvals') api<{ requests: Approval[] }>('approvals').then(v => setRequests(v.requests)).catch(e => setError(e.message)); }, [mode]);
  async function submit(e: React.FormEvent) { e.preventDefault(); setBusy(true); setError(''); try { if (mode === 'create') { const where = '#' + (remote?remote+'/':'') + channel.replace(/^#/, '').trim(); await api('create', { Channel: where, Open: open }); await onDone(where); } else if (mode === 'join') { await api('join', { Invitation: link }); toast('Invitation accepted. Your existing profile is unchanged.'); await onDone(); } else { const v = await api<{ url?: string; name?: string }>('invite', { Channel: channel, Agent: agent, Write: !readOnly }); if (v.url) setLink(v.url); else { toast(`${v.name} can join the conversation now.`); await onDone(); } } } catch (e) { setError(e instanceof Error ? e.message : 'Please try again'); } finally { setBusy(false); } }
  const title = mode === 'create' ? 'Create channel' : mode === 'invite' ? 'Invite collaborator' : mode === 'join' ? 'Accept invitation' : 'Connection requests';
  if(mode==='connect')return <div className="modal-backdrop" onMouseDown={e=>{if(e.target===e.currentTarget)onClose();}}><section className="modal" role="dialog" aria-modal="true" aria-label="Add workspace"><button className="icon close" aria-label="Close dialog" onClick={onClose}><X size={19}/></button><WorkspaceConnect onDone={onDone}/></section></div>;
  return <div className="modal-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose(); }}><section className="modal" role="dialog" aria-modal="true" aria-label={title}><button className="icon close" aria-label="Close dialog" onClick={onClose}><X size={19} /></button><div className="modal-symbol">{mode === 'create' ? <Hash size={24} /> : mode === 'approvals' ? <ShieldCheck size={24} /> : <Users size={24} />}</div><h2>{title}</h2><p>{mode === 'create' ? 'Choose a name and who can read the channel.' : mode === 'invite' ? 'Invite a person or agent to this channel.' : mode === 'join' ? 'Accept an invitation using your current identity.' : 'Review agents requesting workspace access.'}</p>{mode === 'approvals' ? <div className="approvals">{!requests.length && <div className="request-empty"><CheckCheck size={22} />No pending requests.</div>}{requests.map(q => <div className="request" key={q.id}><Avatar name={q.name} /><div><strong>{q.name}</strong><span>{q.channels?.join(', ') || '#general'}</span></div><button className="button primary" onClick={async () => { try { await api('approvals', { Code: q.code, Channels: q.channels?.length ? q.channels : ['#general', '@*'] }); setRequests(prev => prev.filter(x => x.id !== q.id)); toast('Agent connected.'); } catch (e) { setError((e as Error).message); } }}>Admit</button></div>)}</div> : link && mode === 'invite' ? <div className="invite-result"><div className="success-mark"><Check size={22} /></div><strong>Invitation link</strong><p>One use. Valid for 24 hours. Works for a human or an agent.</p><input aria-label="Invitation link" readOnly value={link} onFocus={e => e.target.select()} /><button className="button primary wide" onClick={async () => { await navigator.clipboard.writeText(link); toast('Invitation link copied.'); }}><Copy size={16} />Copy invitation link</button></div> : <form onSubmit={submit}>{mode === 'create' ? <><label>Channel name<div className="input-icon"><Hash size={16} /><input autoFocus required value={channel} onChange={e => setChannel(e.target.value)} placeholder="e.g. project" pattern={String.raw`#?[A-Za-z0-9_.\/\-]+`} /></div></label><label className="checkbox"><input type="checkbox" checked={open} onChange={e => setOpen(e.target.checked)} /><div><strong>Make this channel open</strong><span>Anyone can read it. Private is the default.</span></div><Globe size={17} /></label></> : mode === 'join' ? <label>Invitation link<input autoFocus required value={link} onChange={e => setLink(e.target.value)} placeholder="https://…/join?invite=…" /></label> : <><label>Channel<select value={channel} onChange={e => setChannel(e.target.value)} required>{channels.filter(c => c.write).map(c => <option key={c.name}>{c.name}</option>)}</select></label><label>Already connected? Invite by handle<input autoFocus value={agent} onChange={e => setAgent(e.target.value)} placeholder="@agent-name, or leave empty for a link" /></label><label className="checkbox"><input type="checkbox" checked={readOnly} onChange={e => setReadOnly(e.target.checked)} /><div><strong>Read-only access</strong><span>They can follow the conversation without posting.</span></div></label></>}{error && <div className="form-error" role="alert">{error}</div>}<button className="button primary wide" disabled={busy}>{busy ? 'One moment…' : mode === 'create' ? 'Create channel' : mode === 'join' ? 'Accept invitation' : agent ? 'Invite collaborator' : 'Create invitation link'}<ChevronRight size={16} /></button></form>}</section></div>;
}
createRoot(document.getElementById('root')!).render(<App />);
