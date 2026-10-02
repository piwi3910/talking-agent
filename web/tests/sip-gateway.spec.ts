import {test,expect} from '@playwright/test';
test('gateway settings save, connect, show failures and disconnect',async({page})=>{
 let config={revision:0,host:'',port:5060,transport:'udp',mode:'trunk',advertise_ip:'192.168.10.142',registrations:{},auto_connect:false};let state='disconnected';
 await page.route('**/api/settings/sip**',async route=>{const req=route.request();const url=new URL(req.url());if(req.method()==='POST'){if(url.pathname.endsWith('/connect')){config.auto_connect=true;state='error';}else if(url.pathname.endsWith('/disconnect')){config.auto_connect=false;state='disconnected';config.revision++;}else{config={...req.postDataJSON(),revision:config.revision+1};}}await route.fulfill({json:{config,state,error:state==='error'?'gateway did not respond: check IP, port, transport and firewall':'',sip_port:5060,rtp_start:10000,rtp_end:10199}});});
 await page.goto('/settings');const gateway=page.getByRole('region',{name:'SIP gateway',exact:true});
 await gateway.getByLabel('Gateway IP address').fill('192.168.10.50');await gateway.getByLabel('Gateway SIP port').fill('5061');await gateway.getByLabel('SIP transport').selectOption('tcp');
 await gateway.getByRole('button',{name:'Save gateway settings',exact:true}).click();await expect(gateway.getByRole('status')).toContainText('saved');
 await gateway.getByRole('button',{name:'Connect',exact:true}).click();await expect(gateway.getByRole('alert')).toContainText('gateway did not respond');await expect(gateway.getByLabel('Gateway IP address')).toBeDisabled();
 await gateway.getByRole('button',{name:'Disconnect',exact:true}).click();await expect(gateway.getByLabel('Gateway IP address')).toBeEnabled();
 await gateway.getByLabel('Connection mode').selectOption('register');await expect(gateway.getByLabel('SIP extension',{exact:true})).toHaveCount(3);await expect(gateway.getByLabel('SIP password',{exact:true})).toHaveCount(3);
 await page.setViewportSize({width:390,height:844});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
