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
