package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativePiAndOpenCodeRunIdleTurns(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for runtime extension integration tests")
	}
	for _, runtime := range []string{"pi", "opencode"} {
		t.Run(runtime, func(t *testing.T) {
			r := newRunner(t, "")
			detect, plugin := ".pi/agent", ".pi/agent/extensions/grpvn.js"
			if runtime == "opencode" {
				detect = ".config/opencode"
				plugin = ".config/opencode/plugins/grpvn-doorbell.js"
			}
			if err := os.MkdirAll(filepath.Join(r.home, detect), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.home, "package.json"), []byte(`{"type":"module"}`), 0644); err != nil {
				t.Fatal(err)
			}
			r.mustRun("skill", "install")
			script := filepath.Join(t.TempDir(), "native.mjs")
			if err := os.WriteFile(script, []byte(nativeBridgeTestScript), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, script, r.bin, filepath.Join(r.home, plugin), runtime)
			cmd.Dir = r.cwd
			cmd.Env = r.env
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("native %s bridge: %v\n%s", runtime, err, output)
			}
		})
	}
}

const nativeBridgeTestScript = `
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { pathToFileURL } from "node:url";
const [binary,plugin,runtime]=process.argv.slice(2);
process.env.GRPVN_BIN=binary;
delete process.env.GRPVN_ACTIVATION_MODE;
const execute=promisify(execFile);
const run=async(...args)=>(await execute(binary,args,{env:process.env,timeout:10000})).stdout.trim();
const seen=[];
const hooks=new Map();
const mod=await import(pathToFileURL(plugin));
let close;
if(runtime==="pi"){
 mod.default({on:(name,fn)=>hooks.set(name,fn),sendMessage:(message,options)=>seen.push({message,options})});
 await hooks.get("session_start")({}, {cwd:process.cwd(),hasUI:false,sessionManager:{getSessionId:()=>"native-pi"}});
 close=()=>hooks.get("session_shutdown")({});
}else{
 const api=await mod.GrpvnDoorbell({directory:process.cwd(),client:{session:{promptAsync:async(request)=>{seen.push(request);}}}});
 await api.event({event:{type:"session.created",properties:{info:{id:"native-opencode"}}}});
 close=()=>api.event({event:{type:"server.instance.disposed"}});
}
const waitFor=async(predicate)=>{const until=Date.now()+10000;while(!await predicate()){if(Date.now()>until)throw Error("native idle delivery timed out");await new Promise(r=>setTimeout(r,50));}};
try{
 const row=(await run("ss","list")).split("\n").find(row=>row.includes(" "+runtime+" "));
 assert.ok(row,"native session must register itself");
 const [registry,agent]=row.split(" ");
 await run("--as","peer","s","@"+agent,"notify without inference");
 await waitFor(()=>seen.length===1);
 if(runtime==="pi"){
  assert.equal(seen[0].options.triggerTurn,false);
  assert.equal(seen[0].options.deliverAs,"nextTurn");
  assert.equal(seen[0].message.customType,"grpvn");
 }else{assert.equal(seen[0].body.noReply,true);}
 await waitFor(async()=>(await run("ss","receipts")).includes("status=accepted"));
 assert.ok((await run("c")).includes("@me"),"native acceptance must not mark DM read");
 await run("ss","mode",registry,"activate");
 await run("--as","peer","s","@"+agent,"start an idle turn");
 await waitFor(()=>seen.length===2);
 if(runtime==="pi"){
  assert.equal(seen[1].options.triggerTurn,true);
  assert.equal(seen[1].options.deliverAs,"followUp");
 }else{assert.equal(seen[1].body.noReply,false);assert.equal(seen[1].path.id,"native-opencode");}
 await waitFor(async()=>(await run("ss","receipts")).split("\n").filter(line=>line.includes("status=accepted")).length===2);
}finally{await close();}
`
