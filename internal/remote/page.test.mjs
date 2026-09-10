import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import assert from 'node:assert/strict';

// Execute the embedded page itself; only browser/network boundaries are fake.
function page() {
  const elements = new Map();
  function element(tag = 'div') {
    return { tagName: tag.toUpperCase(), textContent: '', disabled: false, style: {}, children: [],
      value: '0', attrs: {}, events: {},
      classList: { toggle() {} }, setAttribute(k,v) { this.attrs[k] = String(v); },
      addEventListener(k,fn) { this.events[k] = fn; },
      appendChild(child) { this.children.push(child); },
      removeChild() { this.children.shift(); },
      get firstChild() { return this.children[0]; } };
  }
  const get = id => {
    if (!elements.has(id)) elements.set(id, element());
    return elements.get(id);
  };
  const requests = [];
  const context = vm.createContext({
    document: { hidden: false, getElementById: get, createElement: element, addEventListener() {} },
    setTimeout() { return 1; }, clearTimeout() {},
    fetch(url, options) {
      return new Promise((resolve, reject) => requests.push({ url, options, resolve, reject }));
    },
  });
  const html = readFileSync(new URL('./page.html', import.meta.url), 'utf8');
  const script = html.match(/<script>([\s\S]*?)<\/script>/)[1].replace('{{.Base}}', '"/"');
  vm.runInContext(script, context);
  return { get, requests, run: code => vm.runInContext(code, context) };
}
async function reply(request, body, status = 200) {
  request.resolve({ ok: status === 200, status, json: async () => body });
  await new Promise(resolve => setImmediate(resolve));
}

test('late pre-command poll cannot resurrect stopped playback', async () => {
  const p = page();
  p.run("connected = true");
  p.run('render({playing:true,title:"old"})');
  const stop = p.run('call("stop", "POST")');
  await reply(p.requests[1], { playing: false });
  await stop;
  await reply(p.requests[0], { playing: true, title: 'old' });
  assert.equal(p.get('pause').disabled, true);
  assert.notEqual(p.get('title').textContent, 'old');
});

test('poll cannot re-enable controls while a command is pending', async () => {
  const p = page();
  p.run("connected = true");
  p.run('render({playing:true})');
  const pause = p.run('call("pause", "POST")');
  await reply(p.requests[0], { playing: true });
  assert.equal(p.get('pause').disabled, true);
  await reply(p.requests[1], { playing: true, paused: true });
  await pause;
  assert.equal(p.get('pause').disabled, false);
});

test('visibility polling does not overlap an outstanding status request', () => {
  const p = page();
  p.run("connected = true");
  p.run('poll(); poll()');
  assert.equal(p.requests.length, 1);
});

test('a stale network failure cannot overwrite a successful command', async () => {
  const p = page();
  p.run("connected = true");
  p.get('l-offline').textContent = 'offline';
  const pause = p.run('call("pause", "POST")');
  await reply(p.requests[1], { playing: true, title: 'current' });
  await pause;
  p.requests[0].reject(new Error('network'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(p.get('title').textContent, 'current');
});

test('command queues a fresh poll after an older outstanding poll', async () => {
  const p = page();
  p.run("connected = true");
  const stop = p.run('call("stop", "POST")');
  await reply(p.requests[1], { playing: false });
  await stop;
  assert.equal(p.requests.length, 2);
  await reply(p.requests[0], { playing: true });
  assert.equal(p.requests.length, 3);
  assert.equal(p.requests[2].url, '/status');
});

test('episode buttons remain disabled until command completion', async () => {
  const p = page();
  p.run("connected = true");
  p.run('drawEps([{number:1}])');
  const stop = p.run('call("stop", "POST")');
  assert.equal(p.get('eps').children[0].disabled, true);
  await reply(p.requests[0], { playing: true });
  assert.equal(p.get('eps').children[0].disabled, true);
  await reply(p.requests[1], { playing: false });
  await stop;
  assert.equal(p.get('eps').children[0].disabled, false);
});

for (const status of [200, 409, 500]) {
  test(`stale delayed JSON (${status}) is ignored`, async () => {
    const p = page();
  p.run("connected = true");
    let finishBody;
    p.requests[0].resolve({status, ok: status === 200, json: () => new Promise(resolve => { finishBody = resolve; })});
    await new Promise(resolve => setImmediate(resolve));
    const pause = p.run('call("pause", "POST")');
    await reply(p.requests[1], { playing: true, title: 'current' });
    await pause;
    finishBody({playing:true, title:'stale', error:'stale'});
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(p.get('title').textContent, 'current');
    assert.notEqual(p.get('ep').textContent, 'stale');
  });
}

 test('offline retains metadata and disables every control until status GET recovers', async () => {
 const p = page();
 await reply(p.requests[0], {playing:true,title:'Last title',episode:3,duration_sec:100});
 p.run('drawEps([{number:3}])');
 const command = p.run('call("pause","POST")');
 p.requests[1].reject(new Error('offline'));
 await command;
 assert.equal(p.get('title').textContent,'Last title');
 for (const id of ['pause','epsbtn','session','seek']) assert.equal(p.get(id).disabled,true,id);
 assert.equal(p.get('eps').children[0].disabled,true);
 const count = p.requests.length;
 await p.run('call("next","POST")');
 assert.equal(p.requests.length,count,'never queues offline commands');
 await reply(p.requests[2],{playing:true,title:'Last title',duration_sec:100});
 assert.equal(p.get('pause').disabled,false);
 assert.equal(p.get('seek').disabled,false);
 });

 test('range keeps drag value through poll and commits only on change', async () => {
 const p = page();
 await reply(p.requests[0],{playing:true,duration_sec:100,position_sec:10});
 assert.equal(Number(p.get('seek').value),10);
 p.get('seek').value='65'; p.get('seek').events.input();
 p.run('render({playing:true,duration_sec:100,position_sec:15})');
 assert.equal(Number(p.get('seek').value),65);
 assert.equal(p.requests.length,1);
 p.get('seek').events.change();
 assert.equal(p.requests[1].url,'/seek/65');
 await reply(p.requests[1],{playing:true,duration_sec:100,position_sec:65});
 });

 test('session and toggle accessibility follow authoritative status', async () => {
 const p=page();
 await reply(p.requests[0],{playing:true,episode:0,studio:'Studio',paused:true,session_limited:true,session_remaining:2});
 assert.equal(Number(p.get('session').value),2);
 assert.equal(p.get('pause').attrs['aria-pressed'],'true');
 assert.equal(p.get('stopafter').attrs['aria-pressed'],'false');
 p.get('session').value='3'; p.get('session').events.change();
 assert.equal(p.requests[1].url,'/session/3');
 await reply(p.requests[1],{playing:true,session_limited:true,session_remaining:3});
 });

test('episode fetch failure marks offline but late failures cannot undo a newer command', async () => {
 for (const stale of [false,true]) {
  const p=page();
  await reply(p.requests[0],{playing:true,title:'Current',duration_sec:100});
  const episodes=p.run('loadEps()');
  if(stale){
   const pause=p.run('call("pause","POST")');
   await reply(p.requests[2],{playing:true,title:'Current',duration_sec:100});
   await pause;
  }
  p.requests[1].reject(new Error('network'));
  await episodes;
  assert.equal(p.run('connected'),stale);
  assert.equal(p.get('pause').disabled,!stale);
  assert.equal(p.get('title').textContent,'Current');
 }
});
