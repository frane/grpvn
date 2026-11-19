import { test, expect, type Page } from '@playwright/test';
import { spawn, spawnSync, ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from 'node:net';


async function expectReadableTitle(page:Page){
  const title=page.locator('.conversation-title>span');
  await expect(title).toBeVisible();
  await expect.poll(()=>title.evaluate(element=>element.scrollWidth-element.clientWidth),{message:'A short conversation title must fit without ellipsis'}).toBeLessThanOrEqual(1);
  await expect.poll(()=>page.locator('.toolbar-actions').evaluate(element=>element.getBoundingClientRect().right-window.innerWidth),{message:'Header actions must remain inside the window'}).toBeLessThanOrEqual(0);
  await expect(page.locator('.app-header')).toHaveCount(0);
  if((page.viewportSize()?.width||0)>760)await expect.poll(()=>page.locator('.sidebar').evaluate(sidebar=>{const bounds=sidebar.getBoundingClientRect();return Array.from(sidebar.querySelectorAll('.workspace-head,.main-nav')).every(element=>{const box=element.getBoundingClientRect();return box.left>=bounds.left&&box.right<=bounds.right;});}),{message:'Source and navigation must fit inside the sidebar'}).toBe(true);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
}

let host:ChildProcess;let link:string;let root:string;let bin:string;let env:NodeJS.ProcessEnv;
test.beforeAll(async()=>{
  root=mkdtempSync(join(tmpdir(),'grpvn-ui-browser-'));bin=process.env.GRPVN_TEST_BIN||'/private/tmp/grpvn-ui-dev';
  env={...process.env,GRPVN_DB:join(root,'grpvn.db'),GRPVN_STATE:join(root,'state.json'),GRPVN_DATABASE_URL:'',GRPVN_SCOPE:'host'};
  const port=await new Promise<number>((resolve,reject)=>{const socket=createServer();socket.listen(0,'127.0.0.1',()=>{const port=(socket.address() as {port:number}).port;socket.close(()=>resolve(port));});socket.on('error',reject);});
  const base='http://127.0.0.1:'+port;
  const invited=spawnSync(bin,['iv','--to','Workspace owner','--channel','*','--write','--dm','--url',base],{env,encoding:'utf8',timeout:30000});
  expect(invited.status,invited.stderr).toBe(0);link=invited.stdout.trim();
  host=spawn(bin,['serve','--web','--listen','127.0.0.1:'+port,'--public-url',base],{env,stdio:['ignore','pipe','pipe']});
  await new Promise<void>((resolve,reject)=>{let output='';const timeout=setTimeout(()=>reject(Error('Web coordinator did not start')),20000);host.stderr!.on('data',data=>{output+=data;if(output.includes('coordinator listening')){clearTimeout(timeout);resolve();}});host.on('exit',code=>reject(Error('Web coordinator exited '+code)));});
});
test.afterAll(async()=>{if(host){host.kill('SIGTERM');await new Promise<void>(r=>{host.once('exit',()=>r());setTimeout(r,7000);});}if(root)rmSync(root,{recursive:true,force:true});});

test('human chat, threaded replies, invitations, browser continuity and mobile layout',async({page,browser})=>{
  test.setTimeout(90000);
  const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(link);
  await page.getByLabel('Your handle').fill('frane');
  await page.getByLabel('Password',{exact:true}).fill('a good owner test password');
  await page.getByRole('button',{name:'Join the workspace',exact:true}).first().click();
  await expect(page.getByRole('button',{name:'Workspace menu',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Create channel',exact:true}).first().click();
  await page.getByLabel('Channel name').fill('general');
  await page.getByRole('dialog').getByRole('button',{name:'Create channel',exact:true}).click();
  await expectReadableTitle(page);
  await page.getByRole('button',{name:'Workspace menu',exact:true}).click();await page.getByRole('menuitem',{name:'Connect an agent…',exact:true}).click();await expect(page.getByLabel('Agent connection prompt')).toContainText('/llm.txt');await page.getByRole('button',{name:'Close dialog',exact:true}).click();
  await page.getByRole('button',{name:'Workspace menu',exact:true}).click();await page.getByRole('menuitem',{name:'Appearance…',exact:true}).click();
  await page.getByRole('radio',{name:'Dark',exact:true}).click();await expect(page.locator('html')).toHaveAttribute('data-theme','dark');
  await expect.poll(()=>page.locator('.main').evaluate(element=>getComputedStyle(element).backgroundColor)).not.toBe('rgb(255, 255, 255)');
  await expect.poll(()=>page.locator('.main-nav button.active').evaluate(element=>getComputedStyle(element).backgroundColor)).toBe('rgb(40, 45, 53)');
  await expect.poll(()=>page.locator('button.channel.selected').evaluate(element=>getComputedStyle(element).backgroundColor)).toBe('rgb(40, 45, 53)');await page.screenshot({path:join(tmpdir(),'grpvn-ui-dark.png'),fullPage:true});
  await page.reload();await expect(page.locator('html')).toHaveAttribute('data-theme','dark');
  await page.getByRole('button',{name:'Workspace menu',exact:true}).click();await page.getByRole('menuitem',{name:'Appearance…',exact:true}).click();
  await page.getByRole('radio',{name:'System',exact:true}).click();await page.emulateMedia({colorScheme:'dark'});await expect(page.locator('html')).toHaveAttribute('data-theme','dark');await page.emulateMedia({colorScheme:'light'});await expect(page.locator('html')).toHaveAttribute('data-theme','light');
  await page.getByRole('radio',{name:'Light',exact:true}).click();await expect(page.getByRole('radio',{name:'Light',exact:true})).toBeEnabled();await page.getByRole('button',{name:'Close appearance',exact:true}).click();
  const compose=page.getByRole('textbox',{name:'Message #general'});
  await compose.fill('A shared place for people and agents.');await compose.press('Enter');
  await expect(page.locator('.message-body').filter({hasText:'A shared place for people and agents.'})).toHaveCount(1);
  await page.locator('.message').first().hover();await page.getByRole('button',{name:'Reply to frane'}).click();
  await page.getByRole('textbox',{name:'Reply in thread…'}).fill('Threaded context stays together.');
  await page.getByRole('textbox',{name:'Reply in thread…'}).press('Enter');
  await expect(page.locator('.thread .message-body').filter({hasText:'Threaded context stays together.'})).toBeVisible();
  await page.evaluate(()=>{Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async(value:string)=>{(window as any).__messageLink=value;}}});});
  await page.locator('.thread .message').filter({hasText:'Threaded context stays together.'}).getByRole('button',{name:'Message actions for frane',exact:true}).click();await page.getByRole('menuitem',{name:'Copy message link',exact:true}).click();
  const replyLink=await page.evaluate(()=>(window as any).__messageLink as string);expect(replyLink).toMatch(/\/m\/[0-9A-HJKMNP-TV-Z]{26}$/);
  await page.goto(replyLink);await expect(page.locator('.thread .message.linked-message')).toContainText('Threaded context stays together.');
  await page.getByRole('button',{name:'Close thread'}).click();
  await page.getByRole('button',{name:'Invite',exact:true}).click();
  await page.getByRole('button',{name:'Create invitation link'}).click();
  const invitation=await page.getByLabel('Invitation link').inputValue();
  await page.getByRole('button',{name:'Close dialog'}).click();
  const context=await browser.newContext();const friend=await context.newPage();
  await friend.goto(invitation);await friend.getByLabel('Your handle').fill('alex');await friend.getByLabel('Password',{exact:true}).fill('a good browser test password');
  await friend.getByRole('button',{name:'Join the workspace',exact:true}).first().click();
  await expect(friend.getByRole('textbox',{name:'Message #general'})).toBeVisible();
  await expect(friend.locator('.message-body').filter({hasText:'A shared place for people and agents.'})).toBeVisible();
  await friend.getByRole('textbox',{name:'Message #general'}).fill('Joined directly from the invitation.');await friend.getByRole('textbox',{name:'Message #general'}).press('Enter');
  await expect(page.locator('.message-body').filter({hasText:'Joined directly from the invitation.'})).toBeVisible({timeout:10000});
  const ownerMessage=page.locator('.message').filter({hasText:'A shared place for people and agents.'}).first();
  const friendMessage=friend.locator('.message').filter({hasText:'A shared place for people and agents.'}).first();
  await ownerMessage.hover();await ownerMessage.getByRole('button',{name:'Add reaction',exact:true}).click();await page.getByRole('button',{name:'like thumbs up',exact:true}).click();
  await expect(ownerMessage.locator('.reaction')).toHaveText('👍1');await expect(friendMessage.locator('.reaction')).toHaveText('👍1',{timeout:10000});
  await friendMessage.locator('.reaction').click();await expect(ownerMessage.locator('.reaction')).toHaveText('👍2');
  await ownerMessage.locator('.reaction').click();await expect(friendMessage.locator('.reaction')).toHaveText('👍1');
  await compose.fill('');await compose.press('ArrowUp');await page.getByLabel('Edit message text',{exact:true}).fill('A shared place for people and agents. Corrected.');await page.getByRole('button',{name:'Save changes',exact:true}).click();
  await expect(ownerMessage.locator('.edited-label')).toBeVisible();await expect(friendMessage).toContainText('Corrected.',{timeout:10000});
  await friendMessage.click({button:'right'});await expect(friend.getByRole('menuitem',{name:'Edit message',exact:true})).toHaveCount(0);await friend.keyboard.press('Escape');
  await friend.reload();await expect(friend.getByRole('button',{name:'Workspace menu',exact:true})).toBeVisible();
  await friend.getByRole('button',{name:'Workspace menu',exact:true}).click();await friend.getByRole('menuitem',{name:'Sign out of this workspace',exact:true}).click();await friend.getByLabel('Your handle').fill('alex');await friend.getByLabel('Password',{exact:true}).fill('a good browser test password');await friend.getByRole('button',{name:'Sign in',exact:true}).click();
  await expect(friend.getByRole('textbox',{name:'Message #general'})).toBeVisible();
  await page.getByRole('button',{name:'People & agents'}).click();
  await expect(page.locator('.person-card').filter({hasText:'alex'})).toBeVisible();await page.locator('.person-card').filter({hasText:'alex'}).click();await page.getByRole('button',{name:'Message @alex',exact:true}).click();
  await expectReadableTitle(page);
  await page.getByRole('textbox',{name:'Message alex'}).fill('A direct conversation.');await page.getByRole('textbox',{name:'Message alex'}).press('Enter');
  await expect(page.locator('.message-body').filter({hasText:'A direct conversation.'})).toBeVisible();
  await friend.getByRole('button',{name:'People & agents'}).click();await friend.locator('.person-card').filter({hasText:'frane'}).click();await friend.getByRole('button',{name:'Message @frane',exact:true}).click();await friend.getByRole('textbox',{name:'Message frane',exact:true}).fill('A direct reply clears the open inbox badge.');await friend.getByRole('textbox',{name:'Message frane',exact:true}).press('Enter');await page.bringToFront();await expect(page.locator('.message-body').filter({hasText:'A direct reply clears the open inbox badge.'})).toBeVisible();await expect(page.locator('.person-channel').filter({hasText:'alex'}).locator('b')).toHaveCount(0);
  await context.close();
  await page.locator('button.channel').filter({hasText:'general'}).first().click();
  // An external CLI write must arrive through the same live feed.
  const sent=spawnSync(bin,['--as','reef-skua-df13','s','#general','Agent delivery is visible without refreshing.'],{env,encoding:'utf8',timeout:20000});expect(sent.status,sent.stderr).toBe(0);
  await expect(page.locator('.message-body').filter({hasText:'Agent delivery is visible without refreshing.'})).toBeVisible({timeout:10000});
  await page.screenshot({path:join(root,'desktop.png'),fullPage:true});
  await page.screenshot({path:join(process.env.GRPVN_SCREENSHOT_DIR || tmpdir(),'grpvn-ui-desktop.png'),fullPage:true});
  await page.setViewportSize({width:820,height:850});await expectReadableTitle(page);
  await page.setViewportSize({width:390,height:844});
  await expectReadableTitle(page);
  await expect(page.getByRole('button',{name:'Open sidebar'})).toBeVisible();
  await page.screenshot({path:join(process.env.GRPVN_SCREENSHOT_DIR || tmpdir(),'grpvn-ui-mobile.png'),fullPage:true});
  // Exercise the same loopback host used by the standalone desktop app.
  const nativeRoot=join(root,'desktop');
  const native=spawn(process.env.GRPVN_TEST_UI_HOST||'/private/tmp/grpvn-ui-testhost',[],{env:{...env,GRPVN_DB:join(nativeRoot,'grpvn.db'),GRPVN_STATE:join(nativeRoot,'state.json')},stdio:['ignore','pipe','pipe']});
  try {
    const nativeURL=await new Promise<string>((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('Desktop host did not start')),20000);native.stdout!.on('data',data=>{output+=data;if(output.includes('\n')){clearTimeout(timer);resolve(output.trim());}});native.on('exit',code=>reject(Error('Desktop host exited '+code)));});
    const desktop=await page.context().newPage();await desktop.goto(nativeURL);
    await desktop.getByLabel('Workspace address or invitation').fill(new URL(link).origin);
    await desktop.getByLabel('Workspace name',{exact:true}).fill('Research team');
    const popup=desktop.waitForEvent('popup');
    await desktop.getByRole('button',{name:'Continue',exact:true}).click();
    const signin=await popup;
    await signin.getByRole('button',{name:'Continue as frane'}).click();
    await expect(signin.getByText('Connected',{exact:true})).toBeVisible();
    await expect(desktop.getByRole('textbox',{name:'Message #general'})).toBeVisible({timeout:15000});await expect(desktop.getByRole('button',{name:'Workspace menu',exact:true})).toContainText('Research team');
    await expectReadableTitle(desktop);
    await desktop.getByRole('button',{name:'Workspace menu',exact:true}).click();await desktop.getByRole('menuitem',{name:'Rename workspace…',exact:true}).click();await desktop.getByLabel('Workspace name',{exact:true}).fill('Review room');await desktop.getByRole('button',{name:'Save name',exact:true}).click();await expect(desktop.getByRole('dialog')).toHaveCount(0);await expect(desktop.getByRole('button',{name:'Workspace menu',exact:true})).toContainText('Review room');
    await desktop.evaluate(()=>{(window as any).__drafts=[];(window as any)._wails={invoke:(message:string)=>(window as any).__drafts.push(JSON.parse(message))};});await desktop.getByRole('button',{name:'Invite',exact:true}).click();await desktop.getByLabel('Person or agent').fill('desktop-friend@example.test');await desktop.getByRole('button',{name:'Prepare email invitation',exact:true}).click();expect(await desktop.getByLabel('Invitation link').inputValue()).toMatch(new RegExp('^'+new URL(link).origin+'/join\\?invite='));expect(await desktop.evaluate(()=>(window as any).__drafts.find((command:any)=>command.Type==='openEmailDraft')?.Type)).toBe('openEmailDraft');await desktop.getByRole('button',{name:'Close dialog'}).click();await desktop.evaluate(()=>{delete (window as any)._wails;});

    await desktop.getByRole('button',{name:'Workspace menu',exact:true}).click();await desktop.getByRole('menuitem',{name:'Connect an agent…',exact:true}).click();const setup=await desktop.getByLabel('Agent connection prompt').inputValue();expect(setup).toContain('remote grpvn server '+new URL(link).origin);expect(setup).toContain('join #general on that server');expect(setup).toContain('source aliases can differ between machines');await desktop.getByRole('button',{name:'Close dialog',exact:true}).click();
    await desktop.evaluate(()=>{Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async(value:string)=>{(window as any).__copiedReference=value;}}});});await desktop.getByRole('button',{name:'Channel actions',exact:true}).click();await desktop.getByRole('menuitem',{name:'Copy conversation reference',exact:true}).click();expect(await desktop.evaluate(()=>(window as any).__copiedReference)).toBe('#general on '+new URL(link).origin);
    await desktop.getByRole('textbox',{name:'Message #general'}).fill('Same profile from the desktop.');await desktop.getByRole('textbox',{name:'Message #general'}).press('Enter');
    await expect(page.locator('.message-body').filter({hasText:'Same profile from the desktop.'})).toBeVisible({timeout:15000});
    await expect(desktop.getByRole('button',{name:'Workspace menu',exact:true})).toContainText(new URL(link).host);
    await desktop.getByRole('button',{name:'Workspace menu',exact:true}).click();await desktop.getByRole('menuitem',{name:'Add workspace…',exact:true}).click();
    await desktop.getByLabel('Workspace address or invitation').fill(new URL(link).origin);
    await desktop.getByRole('button',{name:'Continue',exact:true}).click();
    await expect(desktop.getByRole('dialog')).toHaveCount(0);
    await desktop.getByRole('button',{name:'People & agents',exact:true}).click();await desktop.locator('.person-card').filter({hasText:'alex'}).click();await desktop.getByRole('button',{name:'Message @alex',exact:true}).click();
    await expect(desktop.getByRole('textbox',{name:'Message alex',exact:true})).toBeVisible();await desktop.getByRole('textbox',{name:'Message alex',exact:true}).fill('A remote DM from the desktop app.');await desktop.getByRole('textbox',{name:'Message alex',exact:true}).press('Enter');await expect(desktop.locator('.message-body').filter({hasText:'A remote DM from the desktop app.'})).toBeVisible();await expect.poll(()=>desktop.locator('.queued').count()).toBe(0,{timeout:15000});
    await desktop.reload();await desktop.getByRole('button',{name:'People & agents',exact:true}).click();await desktop.locator('.person-card').filter({hasText:'alex'}).click();await desktop.getByRole('button',{name:'Message @alex',exact:true}).click();await expect(desktop.locator('.message-body').filter({hasText:'A remote DM from the desktop app.'})).toBeVisible();await desktop.locator('button.channel').filter({hasText:'general'}).click();
    await desktop.reload();await expect(desktop.getByRole('textbox',{name:'Message #general'})).toBeVisible();await desktop.getByRole('button',{name:'Workspace menu',exact:true}).click();await desktop.getByRole('menuitemradio',{name:'Local chat',exact:true}).click();await expect(desktop.getByRole('heading',{name:'Choose a conversation',exact:true})).toBeVisible();await desktop.getByRole('button',{name:'Workspace menu',exact:true}).click();await desktop.getByRole('menuitem',{name:'Connect an agent…',exact:true}).click();await expect(desktop.getByLabel('Agent connection prompt')).toContainText('grpvn-cli@0.15.0');await desktop.getByRole('button',{name:'Close dialog',exact:true}).click();await desktop.getByRole('button',{name:'Workspace menu',exact:true}).click();await desktop.getByRole('menuitemradio',{name:'Review room',exact:true}).click();await expect(desktop.getByRole('textbox',{name:'Message #general'})).toBeVisible();
    // Exercise native tray routing against the real desktop host. Capture the
    // bridge state, then issue the same actions sent by each OS's native menu.
    await desktop.addInitScript(()=>{(window as any).__tray=[];(window as any)._wails={invoke:(message:string)=>{if(message==='wails:runtime:ready'){(window as any).__ready=true;return;}const command=JSON.parse(message);(window as any).__tray.push(command);if(command.Type==='notificationStatus')queueMicrotask(()=>window.dispatchEvent(new CustomEvent('grpvn:native-notification',{detail:{requestID:command.RequestID,supported:true,granted:false}})));}};});
    await desktop.reload();await expect(desktop.getByRole('textbox',{name:'Message #general'})).toBeVisible();
    const tray=()=>desktop.evaluate(()=>(window as any).__tray.filter((command:any)=>command.Type==='trayState').at(-1));
    await expect.poll(async()=>!!(await tray())?.Ready).toBe(true);
    expect(await desktop.evaluate(()=>(window as any).__ready)).toBe(true);
    const remoteTray=await tray();expect(remoteTray.Workspaces.some((source:any)=>source.ID==='')).toBe(true);expect(remoteTray.Workspace).not.toBe('');
    const action=(action:string,extra:Record<string,unknown>={})=>desktop.evaluate(detail=>window.dispatchEvent(new CustomEvent('grpvn:action',{detail})),{action,...extra});
    await action('workspace',{workspace:''});await expect(desktop.getByRole('heading',{name:'Choose a conversation',exact:true})).toBeVisible();await expect.poll(async()=>(await tray()).Workspace).toBe('');
    await action('workspace',{workspace:remoteTray.Workspace});await expect(desktop.getByRole('textbox',{name:'Message #general'})).toBeVisible();
    await action('search');await expect(desktop.locator('.side-search input')).toBeFocused();
    await action('attention');await expect(desktop.locator('.attention-view')).toBeVisible();
    await action('agent');await expect(desktop.getByLabel('Agent connection prompt')).toBeVisible();await desktop.getByRole('button',{name:'Close dialog',exact:true}).click();
    await action('notifications');await desktop.getByLabel('Notify me about').selectOption('all');await expect(desktop.getByLabel('Notify me about')).toBeEnabled();await expect(desktop.getByLabel('Notify me about')).toHaveValue('all');
    await desktop.getByRole('button',{name:'Close notification settings',exact:true}).click();
    await action('pause-notifications',{paused:true});await expect.poll(async()=>(await tray()).Paused).toBe(true);
    await desktop.reload();await expect.poll(async()=>(await tray())?.Paused).toBe(true);
    await action('pause-notifications',{paused:false});await expect.poll(async()=>(await tray()).Paused).toBe(false);
    await action('notifications');await expect(desktop.getByLabel('Notify me about')).toHaveValue('all');await desktop.getByRole('button',{name:'Close notification settings',exact:true}).click();
    await desktop.close();await signin.close();
  } finally { native.kill('SIGINT');await new Promise<void>(resolve=>{native.once('exit',()=>resolve());setTimeout(resolve,7000);}); }
  expect(errors).toEqual([]);
});

test('first unread entry, latest when read, preserved scroll and shared human badges',async({page,browser})=>{
 test.setTimeout(120000);
 const invite=spawnSync(bin,['iv','--to','Read-state test','--channel','*','--write','--dm','--url',new URL(link).origin],{env,encoding:'utf8',timeout:20000});expect(invite.status,invite.stderr).toBe(0);
 await page.goto(invite.stdout.trim());await page.getByLabel('Your handle').fill('reader');await page.getByLabel('Password',{exact:true}).fill('a durable read state password');await page.getByRole('button',{name:'Join the workspace',exact:true}).first().click();
 await page.getByRole('button',{name:'Workspace menu',exact:true}).click();await page.getByRole('menuitem',{name:'Create channel…',exact:true}).click();await page.getByLabel('Channel name').fill('reading-room');await page.getByRole('dialog').getByRole('button',{name:'Create channel',exact:true}).click();
 await page.getByRole('button',{name:'People & agents',exact:true}).click();
 const send=(body:string)=>{const sent=spawnSync(bin,['--as','viewport-peer','s','#reading-room',body],{env,encoding:'utf8',timeout:20000});expect(sent.status,sent.stderr).toBe(0);return sent.stdout.trim().split(/\s+/)[0];};
 const ids:string[]=[];for(let i=0;i<140;i++)ids.push(send('Viewport message '+i+'\n\nA second paragraph keeps enough history outside the viewport.'));
 const base=new URL(link).origin;const lookup=await page.request.get(base+'/v1/chat/messages?target=%23reading-room&ids='+ids[4]);const fifth=(await lookup.json()).messages[0];
 expect((await page.request.post(base+'/v1/chat/read-markers',{headers:{Origin:base},data:{Target:'#reading-room',Position:fifth.seq}})).ok()).toBe(true);
 const channel=page.locator('button.channel').filter({hasText:'reading-room'});await channel.click();const firstUnread=page.locator('[data-message-id="'+ids[5]+'"]');await expect(firstUnread).toBeInViewport({ratio:.2});await expect(page.locator('[data-message-id="'+ids[139]+'"]')).toHaveCount(0);
 const read=await page.request.get(base+'/v1/chat/messages?target=%23reading-room&ids='+ids[5]);expect((await read.json()).read_position).toBeLessThan(fifth.seq+100);
 const messages=page.locator('.messages');await messages.hover();await page.mouse.wheel(0,-300);const top=await messages.evaluate(element=>element.scrollTop);send('An incoming message must not jump a reader to the end.');await expect.poll(()=>messages.evaluate((element,position)=>Math.abs(element.scrollTop-position),top)).toBeLessThan(3);
 await page.getByRole('button',{name:'People & agents',exact:true}).click();expect((await page.request.post(base+'/v1/chat/read-markers',{headers:{Origin:base},data:{Target:'#reading-room',Resume:true}})).ok()).toBe(true);await channel.click();await expect.poll(()=>messages.evaluate(element=>element.scrollHeight-element.scrollTop-element.clientHeight)).toBeLessThan(4);
 await page.reload();await expect.poll(()=>messages.evaluate(element=>element.scrollHeight-element.scrollTop-element.clientHeight)).toBeLessThan(4);
 await page.getByRole('button',{name:'People & agents',exact:true}).click();
 const mobileContext=await browser.newContext();const mobile=await mobileContext.newPage();
 try{
  await mobile.goto(base);await mobile.getByLabel('Your handle').fill('reader');await mobile.getByLabel('Password',{exact:true}).fill('a durable read state password');await mobile.getByRole('button',{name:'Sign in',exact:true}).click();await mobile.getByRole('button',{name:'People & agents',exact:true}).click();
  const aSession=await (await page.request.get(base+'/v1/auth/session')).json();const bSession=await (await mobile.request.get(base+'/v1/auth/session')).json();expect(bSession.profile.id).toBe(aSession.profile.id);
  send('@reader, one message shared across devices.');await expect(channel.locator('b')).toHaveText('1');
  await mobile.setViewportSize({width:390,height:844});await mobile.getByRole('button',{name:'Open sidebar',exact:true}).click();await mobile.locator('button.channel').filter({hasText:'reading-room'}).click();await mobile.bringToFront();await expect(mobile.getByText('one message shared across devices.',{exact:false})).toBeVisible();await expect(channel.locator('b')).toHaveCount(0,{timeout:10000});
 }finally{await mobileContext.close();}
});
