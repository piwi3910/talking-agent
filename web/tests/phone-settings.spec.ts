import {test,expect} from '@playwright/test';

test('phone settings assign different numbers to every persona and persist after reload',async({page})=>{
 await page.goto('/');
 await page.getByRole('tab',{name:'settings',exact:true}).click();
 const panel=page.getByRole('region',{name:'Phone settings',exact:true});
 const fields=panel.locator('.phone-persona textarea');
 await expect(fields).toHaveCount(3);
 const original=await fields.evaluateAll(nodes=>nodes.map(n=>(n as HTMLTextAreaElement).value));
 try{
  await fields.nth(0).fill('9500\n+329500');
  await fields.nth(1).fill('9501');
  await fields.nth(2).fill('9502');
  await panel.getByRole('button',{name:'Save phone settings',exact:true}).click();
  await expect(panel.getByRole('status')).toContainText('Phone settings saved');
  await page.reload();await page.getByRole('tab',{name:'settings',exact:true}).click();
  await expect(fields.nth(0)).toHaveValue('9500\n+329500');
  await expect(fields.nth(1)).toHaveValue('9501');
  await expect(fields.nth(2)).toHaveValue('9502');
  await expect(panel).not.toContainText('access code');
  await fields.nth(1).fill('9500');
  await panel.getByRole('button',{name:'Save phone settings',exact:true}).click();
  await expect(panel.getByRole('alert')).toContainText('already assigned');
  await panel.getByRole('button',{name:'Reload saved settings',exact:true}).click();
  await expect(fields.nth(1)).toHaveValue('9501');
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:'/tmp/talking-agent-phone-settings.png',fullPage:true});
 } finally {
  for(let i=0;i<original.length;i++)await fields.nth(i).fill(original[i]);
  await panel.getByRole('button',{name:'Save phone settings',exact:true}).click();
  await expect(panel.getByRole('status')).toContainText('Phone settings saved');
 }
});
