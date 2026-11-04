const {test}=require('node:test');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const html=fs.readFileSync(path.join(__dirname,'web/index.html'),'utf8');
test('grouped dashboard preserves edits and upstream embedded API routing',async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 const metrics=[];
 try{
  for(const width of [360,1024,1920])for(const scale of [1,1.25,1.5])for(const prefix of ['', '/admin']){
   const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:scale});
   await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
   const page=await context.newPage(),errors=[],requests=[];page.on('pageerror',e=>errors.push(e.message));
   await page.route('**/*',route=>{
    const url=new URL(route.request().url());requests.push(url.pathname);
    if(url.pathname===prefix+'/')return route.fulfill({contentType:'text/html; charset=utf-8',body:html});
    assert.ok(url.pathname.startsWith(prefix+'/api/'),url.pathname);
    const key=url.pathname.slice(prefix.length),data=key==='/api/me'?{username:'admin'}:key==='/api/tunables'?{xp_gain:'1'}:{sessions:1,players:1,items:1,mails:0,tunables:1,audits:0};
    return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
   });
   await page.clock.install();
   await page.goto('http://127.0.0.1:19214'+prefix+'/');
   await page.locator('#tun_xp_gain').waitFor();
   assert.equal(await page.locator('.tunable-group').count(),5);
   await page.locator('#tun_fish_tv_channel').waitFor();
   await page.locator('#fvt_text').waitFor();
   await page.locator('#tun_xp_gain').fill('2.5');
   await page.clock.fastForward(8100);
   await page.waitForTimeout(100);
   assert.equal(await page.locator('#tun_xp_gain').inputValue(),'2.5');
   for(const text of ['', '最长合理业务参数名称与说明'.repeat(10), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.repeat(15)]){
    if(text)for(const target of [page.locator('.tunable-field label').first(),page.locator('label[for="fvt_text"]')])await target.evaluate((e,t)=>e.textContent=t,text);
    await page.locator('#fvt_text').fill(text||'鱼汛通道测试');
    assert.equal(await page.locator('#fvt_text').inputValue(),text||'鱼汛通道测试');
    const m=await page.evaluate(()=>({viewport:innerWidth,scroll:document.documentElement.scrollWidth,fields:[...document.querySelectorAll('.tunable-field')].map(e=>{
     const input=e.querySelector('input').getBoundingClientRect(),button=e.querySelector('button')?.getBoundingClientRect(),label=e.querySelector('label');
     return {input:input.width,button:button?.width??0,height:button?.height??0,gap:button?button.x-input.right:0,hasButton:!!button,labelScroll:label.scrollWidth,labelWidth:label.clientWidth,font:getComputedStyle(label).fontSize};
    })}));
    assert.ok(m.scroll<=m.viewport+1,JSON.stringify({width,scale,prefix,m}));
    for(const f of m.fields){assert.ok(f.input>=50);if(f.hasButton)assert.ok(f.button>=40&&f.height>=28&&f.gap>=7);assert.ok(f.labelScroll<=f.labelWidth+1);}
    metrics.push({width,scale,prefix,text:!text?'normal':text.startsWith('ABC')?'continuous':'chinese',...m.fields[0]});
    if(process.env.RF4_RULES_SCREENSHOT_DIR&&scale===1.5&&text===''&&prefix==='/admin'&&width!==1024){await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:path.join(process.env.RF4_RULES_SCREENSHOT_DIR,`dashboard-${width}.png`),fullPage:true});}
   }
   await page.locator('#fvt_ch').fill('28');await page.locator('#fvt_kind').fill('0');await page.locator('#fvt_text').fill('鱼汛通道测试');
   const [request]=await Promise.all([page.waitForRequest('**/api/fish_tv_test'),page.getByRole('button',{name:'发送测试',exact:true}).click()]);
   assert.deepEqual(request.postDataJSON(),{uid:0,channel:28,kind:0,text:'鱼汛通道测试'});
   assert.deepEqual(errors,[]);await context.close();
  }
 }finally{await browser.close();}
 console.log(JSON.stringify({cases:metrics.length,measurements:metrics}));
});
