import {test,expect} from '@playwright/test';
import {spawn,spawnSync,ChildProcess} from 'node:child_process';
import {mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createServer} from 'node:net';
import {createHash} from 'node:crypto';
let host:ChildProcess,root:string,base:string,invite:string;
test.beforeAll(async()=>{
 root=mkdtempSync(join(tmpdir(),'grpvn-approvals-'));const bin=process.env.GRPVN_TEST_BIN||'/private/tmp/grpvn-ui-dev';const env={...process.env,GRPVN_DB:join(root,'grpvn.db'),GRPVN_STATE:join(root,'state.json'),GRPVN_DATABASE_URL:'',GRPVN_SCOPE:'host'};
 const port=await new Promise<number>((resolve,reject)=>{const server=createServer();server.listen(0,'127.0.0.1',()=>{const port=(server.address() as any).port;server.close(()=>resolve(port));});server.on('error',reject);});base='http://127.0.0.1:'+port;
 const created=spawnSync(bin,['iv','--to','UI owner','--channel','*','--write','--dm','--url',base],{env,encoding:'utf8',timeout:30000});expect(created.status,created.stderr).toBe(0);invite=created.stdout.trim();
 host=spawn(bin,['sv','--web','--listen','127.0.0.1:'+port,'--public-url',base],{env,stdio:['ignore','pipe','pipe']});await new Promise<void>((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('coordinator did not start')),20000);host.stderr!.on('data',data=>{output+=data;if(output.includes('coordinator listening')){clearTimeout(timer);resolve();}});host.on('exit',()=>reject(Error('coordinator exited')));});
});
test.afterAll(async()=>{host?.kill('SIGTERM');if(host)await new Promise<void>(resolve=>{host.once('exit',()=>resolve());setTimeout(resolve,7000);});if(root)rmSync(root,{recursive:true,force:true});});
test('OAuth connection requests have badges, alerts and working one-click admission',async({page,browser})=>{
 test.setTimeout(90000);const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.addInitScript(()=>{const host=window as any;host.__notifications=[];host.Notification=class{static permission='granted';static requestPermission=async()=>'granted';data:any;onclick:any;constructor(public title:string,public options:any){this.data=options.data;host.__notifications.push(this);}close(){}};Object.defineProperty(document,'hasFocus',{configurable:true,value:()=>false});});
 await page.goto(invite);await page.getByLabel('Your handle').fill('owner');await page.getByLabel('Password',{exact:true}).fill('a durable human owner password');await page.getByRole('button',{name:'Join the workspace',exact:true}).first().click();
 await expect(page.getByRole('textbox',{name:'Message #general'})).toBeVisible();await expect(page.locator('.toolbar-detail')).toContainText('Public channel');
 await page.getByRole('button',{name:'Workspace menu',exact:true}).click();await page.getByRole('menuitem',{name:'Notification settings…',exact:true}).click();await page.getByRole('button',{name:'Enable notifications',exact:true}).click();await expect(page.getByRole('button',{name:'Turn off',exact:true})).toBeVisible();await page.getByRole('button',{name:'Close notification settings',exact:true}).click();
 const agentContext=await browser.newContext();const agent=await agentContext.newPage();
 // A loopback callback keeps this test fully local, including HTTP redirects.
 const redirect=base+'/claude-callback';const registered=await agentContext.request.post(base+'/oauth/register',{data:{client_name:'Claude',redirect_uris:[redirect]}});expect(registered.status()).toBe(201);const client=(await registered.json()).client_id;
 const verifier='p'.repeat(64);const query=new URLSearchParams({client_id:client,redirect_uri:redirect,response_type:'code',code_challenge_method:'S256',code_challenge:createHash('sha256').update(verifier).digest('base64url'),scope:'grpvn.read grpvn.write',resource:base+'/mcp',state:'claude-connect'});
 const authorize=base+'/oauth/authorize?'+query;
 await agent.goto(authorize);await expect(agent.getByText(/Waiting for an invitation or approval/)).toBeVisible();
 await expect(page.getByLabel('1 pending connection requests',{exact:true})).toBeVisible();const needs=page.locator('.main-nav button').filter({hasText:'Needs you'});await expect(needs).toContainText('1');
 await expect.poll(()=>page.evaluate(()=>(window as any).__notifications.length)).toBe(1);const alert=await page.evaluate(()=>({title:(window as any).__notifications[0].title,data:(window as any).__notifications[0].data}));expect(alert.title).toContain('wants to connect');expect(alert.data.action).toBe('requests');
 const first=(await (await page.request.get(base+'/v1/access-requests')).json()).requests;expect(first).toHaveLength(1);expect(first[0].channels).toEqual([]);const profile=first[0].profile_id;
 // Retrying sign-in keeps one identity, one pending badge and one alert. The
 // approval action must use the most recent connection code for that profile.
 await agent.goto(authorize);await expect(agent.getByText(/Waiting for an invitation or approval/)).toBeVisible();
 await expect.poll(async()=>{const result=await (await page.request.get(base+'/v1/access-requests')).json();return result.requests[0]?.code;}).not.toBe(first[0].code);
 const current=(await (await page.request.get(base+'/v1/access-requests')).json()).requests;expect(current).toHaveLength(1);expect(current[0].profile_id).toBe(profile);expect(current[0].notified).toBe(true);
 await needs.click();await expect(page.getByRole('region',{name:'Pending connection requests'})).toContainText(current[0].name);
 await page.evaluate(data=>window.dispatchEvent(new CustomEvent('grpvn:open-notification',{detail:data})),alert.data);
 const dialog=page.getByRole('dialog',{name:'Connection requests',exact:true});await expect(dialog).toBeVisible();await expect(dialog).toContainText('Claude');await expect(dialog).toContainText('127.0.0.1');await expect(dialog).toContainText('#general and direct messages');await expect(dialog).not.toContainText('oauth:');
 await dialog.getByRole('button',{name:'Admit',exact:true}).click();await expect(dialog).toContainText('No pending requests.');await expect(page.getByLabel('1 pending connection requests',{exact:true})).toHaveCount(0);await expect(dialog.getByRole('alert')).toHaveCount(0);
 await expect(agent).toHaveURL(new RegExp(base+'/claude-callback\\?'), {timeout:15000});const code=new URL(agent.url()).searchParams.get('code');expect(code).toBeTruthy();
 const exchanged=await agentContext.request.post(base+'/oauth/token',{form:{grant_type:'authorization_code',client_id:client,redirect_uri:redirect,code:code!,code_verifier:verifier,resource:base+'/mcp'}});expect(exchanged.status()).toBe(200);const token=(await exchanged.json()).access_token;
 const headers={Authorization:'Bearer '+token};const identity=await agentContext.request.get(base+'/v1/auth/session',{headers});expect((await identity.json()).profile.id).toBe(profile);
 const sent=await agentContext.request.post(base+'/v1/chat/messages',{headers,data:{Target:'#general',Body:'Claude connected and can collaborate.',IdempotencyKey:'claude-admission-intro'}});expect(sent.status()).toBe(201);
 const messages=await agentContext.request.get(base+'/v1/chat/messages?target=%23general',{headers});expect((await messages.json()).messages.some((message:any)=>message.body==='Claude connected and can collaborate.')).toBe(true);
 expect(await page.evaluate(()=>(window as any).__notifications.length)).toBe(1);expect(errors).toEqual([]);await agentContext.close();
});
