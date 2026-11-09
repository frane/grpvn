"use strict";
const {test} = require("node:test");
const assert = require("node:assert/strict");
const {spawnSync} = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const version = require("../package.json").version;

test("launcher retries transient downloads, verifies checksums and reuses its cache", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "grpvn-launcher-test-"));
  try {
    const exe = process.platform === "win32" ? "grpvn.exe" : "grpvn";
    const fixture = path.join(root, "fixture"); fs.mkdirSync(fixture);
    fs.copyFileSync(process.env.GRPVN_TEST_BIN || "/private/tmp/grpvn-ui-dev", path.join(fixture, exe));
    const platform = {darwin:"darwin",linux:"linux",win32:"windows"}[process.platform];
    const arch = {arm64:"arm64",x64:"x86_64"}[process.arch];
    const asset = `grpvn_${version}_${platform}_${arch}.${process.platform === "win32" ? "zip" : "tar.gz"}`;
    const archive = path.join(root,asset);
    const tar = spawnSync("tar",["-a","-cf",archive,"-C",fixture,exe],{encoding:"utf8"});
    assert.equal(tar.status,0,tar.stderr);
    const digest = crypto.createHash("sha256").update(fs.readFileSync(archive)).digest("hex");
    const mock = path.join(root,"mock.cjs"), log = path.join(root,"requests.log");
    fs.writeFileSync(mock, `const fs=require('node:fs');let count=0;global.fetch=async(url,options)=>{
      fs.appendFileSync(${JSON.stringify(log)},url+'\\n');
      if(options.headers['Accept']==='application/vnd.github+json'){
        if(++count<3&&!process.env.BAD_CHECKSUM)return new Response('',{status:503});
        return new Response(JSON.stringify({assets:[{name:${JSON.stringify(asset)},url:'https://api.github.com/assets/binary'},{name:'checksums.txt',url:'https://api.github.com/assets/checksums'}]}));
      }
      if(options.headers.Accept!=='application/octet-stream')throw Error('Asset negotiation missing');
      if(url.endsWith('/binary'))return new Response(fs.readFileSync(${JSON.stringify(archive)}));
      if(url.endsWith('/checksums'))return new Response((process.env.BAD_CHECKSUM?'0'.repeat(64):${JSON.stringify(digest)})+'  '+${JSON.stringify(asset)}+'\\n');
      throw Error('Unexpected network request');};`);
    const run = (folder,bad=false) => {
      const pkg=path.join(root,folder);fs.mkdirSync(path.join(pkg,"bin"),{recursive:true});
      fs.copyFileSync(path.join(__dirname,"../bin/grpvn.js"),path.join(pkg,"bin/grpvn.js"));
      fs.writeFileSync(path.join(pkg,"package.json"),JSON.stringify({version}));
      return spawnSync(process.execPath,["--require",mock,path.join(pkg,"bin/grpvn.js"),"version"],{env:{...process.env,BAD_CHECKSUM:bad?"1":""},encoding:"utf8",timeout:30000});
    };
    const first=run("good");assert.equal(first.status,0,first.stderr);assert.ok(first.stdout.startsWith(version),first.stdout);
    const requests=fs.readFileSync(log,"utf8").trim().split("\n");assert.equal(requests.length,5);
    const cached=run("good");assert.equal(cached.status,0,cached.stderr);assert.equal(fs.readFileSync(log,"utf8").trim().split("\n").length,5);
    const corrupt=run("bad",true);assert.equal(corrupt.status,1);assert.match(corrupt.stderr,/sha256 mismatch/);
    assert.equal(fs.existsSync(path.join(root,"bad/dist",exe)),false);
  } finally { fs.rmSync(root,{recursive:true,force:true}); }
});
