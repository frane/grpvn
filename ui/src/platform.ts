// Platform actions stay in the desktop bridge. The browser build uses normal
// browser navigation; the shared chat API never opens programs on a computer.
type NativeWindow = Window & {
  _wails?: { invoke: (message: string) => void };
  chrome?: { webview?: { postMessage: (message: string) => void } };
  webkit?: { messageHandlers?: { external?: { postMessage: (message: string) => void } } };
};
export function openExternal(address: string) {
  const target = new URL(address);
  if (target.protocol !== 'https:' && target.protocol !== 'http:') throw new Error('Invalid address');
  const host = window as NativeWindow;
  const message = JSON.stringify({ Type: 'openExternal', URL: target.href });
  if (host._wails?.invoke) host._wails.invoke(message);
  else if (host.chrome?.webview) host.chrome.webview.postMessage(message);
  else if (host.webkit?.messageHandlers?.external) host.webkit.messageHandlers.external.postMessage(message);
  else window.open(target.href, 'grpvn-signin');
}

export function openEmailDraft(to:string,subject:string,body:string){
 // mailto uses URI encoding, not HTML form encoding: Mail displays '+' literally.
 const target='mailto:'+encodeURIComponent(to)+'?subject='+encodeURIComponent(subject)+'&body='+encodeURIComponent(body.replace(/\r?\n/g,'\r\n'));
 const host=window as NativeWindow;const message=JSON.stringify({Type:'openEmailDraft',URL:target});
 if(host._wails?.invoke)host._wails.invoke(message);
 else if(host.chrome?.webview)host.chrome.webview.postMessage(message);
 else if(host.webkit?.messageHandlers?.external)host.webkit.messageHandlers.external.postMessage(message);
 else window.location.href=target;
}

export type NotificationPermissionState={supported:boolean;granted:boolean;error?:string};
export type ChatNotification={id:string;title:string;body:string;target:string;root:string;sound:boolean};
function nativeNotification(command:Record<string,unknown>):Promise<NotificationPermissionState>|null{
 const host=window as NativeWindow;
 const send=host._wails?.invoke?host._wails.invoke.bind(host._wails):host.chrome?.webview?.postMessage?host.chrome.webview.postMessage.bind(host.chrome.webview):host.webkit?.messageHandlers?.external?.postMessage?.bind(host.webkit.messageHandlers.external);
 if(!send)return null;
 const requestID=crypto.randomUUID();
 return new Promise((resolve,reject)=>{
  const timer=setTimeout(()=>{window.removeEventListener('grpvn:native-notification',received);reject(Error('The operating system did not respond. Check notification settings.'));},45000);
  const received=(event:Event)=>{const result=(event as CustomEvent).detail;if(result.requestID!==requestID)return;clearTimeout(timer);window.removeEventListener('grpvn:native-notification',received);if(result.error)reject(Error(result.error));else resolve(result);};
  window.addEventListener('grpvn:native-notification',received);send(JSON.stringify({...command,RequestID:requestID}));
 });
}
export async function notificationPermission():Promise<NotificationPermissionState>{
 const native=nativeNotification({Type:'notificationStatus'});if(native)return native;
 return {supported:'Notification' in window,granted:'Notification' in window&&Notification.permission==='granted'};
}
export async function requestNotificationPermission():Promise<NotificationPermissionState>{
 const native=nativeNotification({Type:'requestNotifications'});if(native)return native;
 if(!('Notification' in window))return {supported:false,granted:false};
 return {supported:true,granted:await Notification.requestPermission()==='granted'};
}
export async function sendChatNotification(options:ChatNotification){
 const native=nativeNotification({Type:'notify',ID:options.id,Title:options.title,Body:options.body,Target:options.target,Root:options.root,Sound:options.sound});
 if(native){const result=await native;if(!result.granted)throw Error('Notifications are disabled in system settings.');return;}
 if(!('Notification' in window)||Notification.permission!=='granted')throw Error('Notifications are disabled in browser settings.');
 const notification=new Notification(options.title,{body:options.body,tag:options.id,icon:'/favicon.svg',silent:!options.sound,data:{target:options.target,root:options.root}});
 notification.onclick=()=>{window.focus();window.dispatchEvent(new CustomEvent('grpvn:open-notification',{detail:notification.data}));notification.close();};
}
export function setUnreadBadge(count:number){
 document.title=(count>0?'('+count+') ':'')+'grpvn';
 const host=window as NativeWindow;const message=JSON.stringify({Type:'notificationBadge',Count:count});
 if(host._wails?.invoke)host._wails.invoke(message);
 else if(host.chrome?.webview)host.chrome.webview.postMessage(message);
 else if(host.webkit?.messageHandlers?.external)host.webkit.messageHandlers.external.postMessage(message);
 else{const navigatorBadge=navigator as Navigator&{setAppBadge?:(count:number)=>Promise<void>;clearAppBadge?:()=>Promise<void>};void (count?navigatorBadge.setAppBadge?.(count):navigatorBadge.clearAppBadge?.())?.catch(()=>{});}
}
