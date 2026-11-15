import {test,expect} from '@playwright/test';
import {spawn,spawnSync,ChildProcess} from 'node:child_process';
import {mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createServer} from 'node:net';

let host:ChildProcess,root:string,bin:string,base:string,invite:string,env:NodeJS.ProcessEnv;
test.beforeAll(async()=>{
 root=mkdtempSync(join(tmpdir(),'grpvn-attention-'));bin=process.env.GRPVN_TEST_BIN||'/private/tmp/grpvn-ui-dev';env={...process.env,GRPVN_DB:join(root,'grpvn.db'),GRPVN_STATE:join(root,'state.json'),GRPVN_DATABASE_URL:'',GRPVN_SCOPE:'host'};
 const port=await new Promise<number>((resolve,reject)=>{const server=createServer();server.listen(0,'127.0.0.1',()=>{const port=(server.address() as any).port;server.close(()=>resolve(port));});server.on('error',reject);});base='http://127.0.0.1:'+port;
 const created=spawnSync(bin,['iv','--to','UI owner','--channel','*','--write','--dm','--url',base],{env,encoding:'utf8',timeout:30000});expect(created.status,created.stderr).toBe(0);invite=created.stdout.trim();
 host=spawn(bin,['sv','--web','--listen','127.0.0.1:'+port,'--public-url',base],{env,stdio:['ignore','pipe','pipe']});await new Promise<void>((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('coordinator did not start')),20000);host.stderr!.on('data',data=>{output+=data;if(output.includes('coordinator listening')){clearTimeout(timer);resolve();}});host.on('exit',()=>reject(Error('coordinator exited')));});
});
test.afterAll(async()=>{host?.kill('SIGTERM');if(host)await new Promise<void>(resolve=>{host.once('exit',()=>resolve());setTimeout(resolve,7000);});if(root)rmSync(root,{recursive:true,force:true});});

test('attention inbox, full threads and grouped opt-in notifications',async({page})=>{
 test.setTimeout(150000);const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.addInitScript(()=>{const host=window as any;host.__notifications=[];host.Notification=class{static permission='granted';static requestPermission=async()=>'granted';data:any;onclick:any;constructor(public title:string,public options:any){this.data=options.data;host.__notifications.push(this);}close(){}};});
 await page.goto(invite);await page.getByLabel('Your handle').fill('frane');await page.getByLabel('Password',{exact:true}).fill('a durable human owner password');await page.getByRole('button',{name:'Join the workspace',exact:true}).first().click();
 await page.getByRole('button',{name:'Create channel',exact:true}).first().click();await page.getByLabel('Channel name').fill('dev');await page.getByRole('dialog').getByRole('button',{name:'Create channel',exact:true}).click();
 const compose=page.getByRole('textbox',{name:'Message #dev'});await compose.fill('Original decision that needs review.');await compose.press('Enter');
 const original=page.locator('article.message').filter({hasText:'Original decision that needs review.'});await expect(original).toBeVisible();const rootID=await original.getAttribute('data-message-id');
 const send=(target:string,body:string,question=false)=>{const result=spawnSync(bin,['--as','rook',question?'q':'s',target,body],{env,encoding:'utf8',timeout:30000});expect(result.status,result.stderr).toBe(0);return result.stdout;};
 send(rootID!,'The review is ready.');await expect(original.getByRole('button',{name:/1 reply.*1 new/})).toBeVisible();
 const inboxButton=page.locator('.main-nav button').filter({hasText:'Needs you'});await expect(inboxButton).toContainText('1');await original.getByRole('button',{name:/1 reply/}).click();await expect(page.locator('.thread')).toContainText('Original decision that needs review.');await expect(page.locator('.thread')).toContainText('The review is ready.');await expect(page.getByRole('button',{name:'Unfollow thread',exact:true})).toBeVisible();await expect(inboxButton.locator('span')).toHaveText('');await page.getByRole('button',{name:'Close thread',exact:true}).click();
 // Move the original out of the recent channel page. Its next reply must
 // still open a complete thread from the inbox, rather than an orphan reply.
 for(let i=0;i<105;i++)send('#dev','Routine progress '+i);
 await page.reload();await expect(compose).toBeVisible();await expect(page.locator('.messages').getByText('Original decision that needs review.',{exact:true})).toHaveCount(0);
 send(rootID!,'Review after the original scrolled away.');await expect(inboxButton).toContainText('1');await inboxButton.click();await expect(page.getByRole('heading',{name:'Needs you',exact:true})).toBeVisible();await page.locator('.attention-open').filter({hasText:'Review after the original scrolled away.'}).click();await expect(page.locator('.thread')).toContainText('Original decision that needs review.');await expect(page.locator('.thread')).toContainText('Review after the original scrolled away.');await page.getByRole('button',{name:'Close thread',exact:true}).click();
 await page.getByRole('button',{name:'Workspace menu',exact:true}).click();await page.getByRole('menuitem',{name:'Notification settings…',exact:true}).click();await expect(page.getByRole('button',{name:'Enable notifications',exact:true})).toBeVisible();await page.getByRole('button',{name:'Enable notifications',exact:true}).click();await expect(page.getByRole('button',{name:'Turn off',exact:true})).toBeVisible();await page.getByRole('button',{name:'Close notification settings',exact:true}).click();
 await page.evaluate(()=>Object.defineProperty(document,'hasFocus',{configurable:true,value:()=>false}));
 for(let i=0;i<3;i++)send(rootID!,'Private review detail '+i);
 await expect.poll(()=>page.evaluate(()=>(window as any).__notifications.length)).toBe(1);
 const delivered=await page.evaluate(()=>{const notification=(window as any).__notifications[0];return {title:notification.title,options:notification.options};});expect(delivered.title).toContain('3 updates');expect(delivered.options.body).not.toContain('Private review detail');expect(delivered.options.data.root).toBe(rootID);expect(delivered.options.data.target).toBe('#dev');
 await page.evaluate(()=>{Object.defineProperty(document,'hasFocus',{configurable:true,value:()=>true});(window as any).__notifications[0].onclick();});await expect(page.locator('.thread')).toContainText('Private review detail 2');await expect(inboxButton.locator('span')).toHaveText('');await page.getByRole('button',{name:'Close thread',exact:true}).click();
 // The same frontend must use the native bridge in the desktop apps.
 await page.evaluate(()=>{const host=window as any;host.__native=[];host._wails={invoke:(message:string)=>{const command=JSON.parse(message);host.__native.push(command);if(command.Type!=='notificationBadge')queueMicrotask(()=>window.dispatchEvent(new CustomEvent('grpvn:native-notification',{detail:{requestID:command.RequestID,supported:true,granted:true}})));}};Object.defineProperty(document,'hasFocus',{configurable:true,value:()=>false});});
 send('#dev','@frane, a decision needs you.');await expect.poll(()=>page.evaluate(()=>(window as any).__native.filter((command:any)=>command.Type==='notify').length)).toBe(1);const native=await page.evaluate(()=>(window as any).__native.find((command:any)=>command.Type==='notify'));expect(native.Target).toBe('#dev');expect(native.Root).toMatch(/^[0-9A-HJKMNP-TV-Z]{26}$/);expect(native.Body).not.toContain('a decision needs you');
 await page.evaluate(()=>{delete (window as any)._wails;Object.defineProperty(document,'hasFocus',{configurable:true,value:()=>true});});
 await inboxButton.click();await expect(page.locator('.attention-open').filter({hasText:'a decision needs you.'})).toBeVisible();await page.screenshot({path:join(tmpdir(),'grpvn-needs-you.png')});await page.getByRole('button',{name:'Mark all read',exact:true}).click();await expect(inboxButton.locator('span')).toHaveText('');
 await page.getByRole('tab',{name:'Following threads',exact:true}).click();await expect(page.locator('.attention-open').filter({hasText:'Original decision that needs review.'})).toBeVisible();
 await page.setViewportSize({width:390,height:844});await expect(page.getByRole('button',{name:'Open sidebar',exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(errors).toEqual([]);
});
