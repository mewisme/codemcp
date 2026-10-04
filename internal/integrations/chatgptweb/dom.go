package chatgptweb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/integrations/browser"
)

type domSnapshot struct {
	Origin                  string `json:"origin"`
	Path                    string `json:"path"`
	TemporaryChat           bool   `json:"temporary_chat"`
	ComposerCount           int    `json:"composer_count"`
	ComposerText            string `json:"composer_text"`
	UserTurns               int    `json:"user_turns"`
	AssistantTurns          int    `json:"assistant_turns"`
	LatestAssistantText     string `json:"latest_assistant_text"`
	CompletionActionVisible bool   `json:"completion_action_visible"`
	Generating              bool   `json:"generating"`
	ToolActive              bool   `json:"tool_active"`
	RateLimited             bool   `json:"rate_limited"`
	SessionExpired          bool   `json:"session_expired"`
	UpstreamError           bool   `json:"upstream_error"`
	SendVisible             bool   `json:"send_visible"`
	SendEnabled             bool   `json:"send_enabled"`
}

type controlResult struct {
	AvailableModels []string `json:"available_models"`
	ModelVerified   bool     `json:"model_verified"`
	EffortVerified  bool     `json:"effort_verified"`
	EffortValue     int      `json:"effort_value"`
}

type promptAttachResult struct {
	Text           string `json:"text"`
	ConnectorCount int    `json:"connector_count"`
}

type sendResult struct {
	Activated bool `json:"activated"`
}

type stopResult struct {
	Stopped bool `json:"stopped"`
}

func jsString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func visiblePrelude() string {
	return `const visible=(el)=>{if(!el||!el.isConnected)return false;for(let n=el;n;n=n.parentElement){if(n.hidden||n.hasAttribute('inert')||n.getAttribute('aria-hidden')==='true')return false;const s=getComputedStyle(n);if(s.display==='none'||s.visibility==='hidden'||s.opacity==='0')return false;}const r=el.getBoundingClientRect();return r.width>0||r.height>0;};`
}

func domSnapshotExpression() string {
	return fmt.Sprintf(`/*codemcp:surface*/(async()=>{%s
const composers=[...document.querySelectorAll(%s)].filter(visible);
const composer=composers.length===1?composers[0]:null;
const form=composer?.closest('form')??null;
const users=[...document.querySelectorAll(%s)].filter(visible);
const assistants=[...document.querySelectorAll(%s)].filter(visible);
const latest=assistants.length?assistants[assistants.length-1]:null;
const answerRootSelector='.markdown, [data-message-author-role="assistant"] .puik-root.not-markdown > [class*="_DilResponseRoot"], [data-markdown-text-style="assistant-message"]';
const answerCandidates=latest?[...latest.querySelectorAll(answerRootSelector)].filter(visible).filter(candidate=>!candidate.parentElement?.closest(answerRootSelector)):[];
const statusContainers=latest?[...latest.querySelectorAll('[data-streaming-response-status]')].filter(visible):[];
const firstStatus=statusContainers[0];
const activityContainers=latest?[...latest.querySelectorAll('[data-chatgpt-agent-turn-start]')].map(marker=>marker.parentElement).filter(Boolean):[];
const answerRoots=answerCandidates.filter(candidate=>!candidate.closest('[data-streaming-response-status]')&&!candidate.closest('[data-testid^="cot-v5"]')&&!(firstStatus&&(candidate.compareDocumentPosition(firstStatus)&Node.DOCUMENT_POSITION_FOLLOWING))&&!(!candidate.closest('[data-content-search-unit-key]')&&activityContainers.some(container=>container.contains(candidate))));
const answerText=answerRoots.map(root=>(root.innerText||root.textContent||'').trim()).filter(Boolean).join('\n\n').trim();
const completion=latest?[...document.querySelectorAll(%s)].filter(visible).some(el=>latest.compareDocumentPosition(el)&Node.DOCUMENT_POSITION_FOLLOWING):false;
const stop=form?[...form.querySelectorAll(%s)].filter(visible):[];
const send=form?[...form.querySelectorAll(%s)].filter(visible):[];
const tool=latest?[...latest.querySelectorAll('[data-testid*="tool"], [data-tool-call-id], [data-mcp-tool], [data-state="running"], [data-testid*="connector"]')].some(visible):false;
const alerts=[...document.querySelectorAll('[role="alert"],[role="dialog"]')].filter(visible).map(el=>(el.textContent||'').replace(/\s+/g,' ').trim());
return {origin:location.origin,path:location.pathname,temporary_chat:location.origin==='https://chatgpt.com'&&location.pathname==='/'&&new URL(location.href).searchParams.get('temporary-chat')==='true',composer_count:composers.length,composer_text:(composer?.textContent||'').trim(),user_turns:users.length,assistant_turns:assistants.length,latest_assistant_text:answerText,completion_action_visible:completion,generating:stop.length===1,tool_active:tool,rate_limited:alerts.some(t=>/too many requests|making requests too quickly/i.test(t)),session_expired:alerts.some(t=>/session has expired/i.test(t)),upstream_error:Boolean(document.querySelector('[data-testid="regenerate-thread-error-button"]'))||alerts.some(t=>/something went wrong[\s\S]*help\.openai\.com/i.test(t)),send_visible:send.length===1,send_enabled:send.length===1&&!send[0].disabled&&send[0].getAttribute('aria-disabled')!=='true'};})()`,
		visiblePrelude(), jsString(ComposerSelector), jsString(UserTurnSelector), jsString(AssistantTurnSelector), jsString(CompletionActionSelector), jsString(StopButtonSelector), jsString(SendButtonSelector))
}

func inspectDOM(ctx context.Context, tab browser.BrowserTab) (domSnapshot, error) {
	var snapshot domSnapshot
	if err := tab.Evaluate(ctx, domSnapshotExpression(), &snapshot); err != nil {
		return domSnapshot{}, err
	}
	return snapshot, nil
}

func configureControlsExpression(model string, effortIndex int, effortLabel string) string {
	return fmt.Sprintf(`/*codemcp:controls*/(async()=>{%s
const norm=s=>(s||'').replace(/\\s+/g,' ').trim();
const modelKey=s=>norm(s).replace(/\\s+Pro$/i,'').toLowerCase();
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
const requestedModel=%s;
const requestedEffortIndex=%d;
const requestedEffortLabel=%s;
const findControl=()=>{const composers=[...document.querySelectorAll(%s)].filter(visible);if(composers.length!==1)throw new Error('expected one visible composer');const form=composers[0].closest('form');if(!form)throw new Error('composer form missing');const controls=[...form.querySelectorAll(%s)].filter(visible);const preferred=controls.filter(el=>el.getAttribute('data-testid')==='model-switcher-dropdown-button'||el.getAttribute('data-codex-intelligence-trigger')==='true');return preferred.length===1?preferred[0]:(controls.length===1?controls[0]:null);};
const open=async()=>{const control=findControl();if(!control)throw new Error('model/effort control unavailable or ambiguous');if(control.getAttribute('aria-expanded')!=='true'&&control.getAttribute('data-state')!=='open'){control.click();await sleep(120);}const menus=[...document.querySelectorAll(%s)].filter(visible).filter(m=>m.querySelector(%s)||m.querySelector(%s));if(menus.length!==1)throw new Error('model/effort menu unavailable or ambiguous');return menus[0];};
let available=[];let modelVerified=requestedModel==='';let effortVerified=requestedEffortIndex<0;let effortValue=-1;
if(requestedModel!==''||requestedEffortIndex>=0){let menu=await open();const modelRows=()=>[...menu.querySelectorAll(%s)].filter(visible);const matchingModelRows=()=>modelRows().filter(el=>modelKey(el.innerText||el.textContent)===modelKey(requestedModel));available=modelRows().map(el=>norm(el.innerText||el.textContent)).filter(Boolean);
if(requestedModel!==''){let rows=matchingModelRows();if(rows.length===0){const views=[...menu.querySelectorAll('[data-model-picker-view]')].filter(visible);if(views.length>1)throw new Error('model picker view is ambiguous');if(views.length===1&&views[0].getAttribute('data-model-picker-view')==='simple'){const toggles=[...views[0].querySelectorAll('[data-model-picker-view-toggle="true"][aria-hidden="false"]')].filter(visible);if(toggles.length!==1)throw new Error('advanced model picker toggle unavailable or ambiguous');toggles[0].click();await sleep(150);menu=await open();}else if(views.length===0){const triggers=[...menu.querySelectorAll('[role="menuitem"][aria-expanded][aria-hidden="false"]')].filter(visible);if(triggers.length===1&&triggers[0].getAttribute('aria-expanded')==='false'){triggers[0].click();await sleep(150);menu=await open();}}rows=matchingModelRows();}if(rows.length!==1)throw new Error('requested model row unavailable or ambiguous');if(rows[0].getAttribute('aria-disabled')==='true')throw new Error('requested model is disabled');if(rows[0].getAttribute('aria-checked')!=='true'){rows[0].click();await sleep(150);menu=await open();rows=matchingModelRows();}modelVerified=rows.length===1&&rows[0].getAttribute('aria-checked')==='true';if(!modelVerified)throw new Error('requested model could not be verified');}
if(requestedEffortIndex>=0){menu=await open();let rows=[...menu.querySelectorAll(%s)].filter(visible).filter(el=>norm(el.innerText||el.textContent)===requestedEffortLabel);if(rows.length===1){if(rows[0].getAttribute('aria-disabled')==='true')throw new Error('requested effort is disabled');if(rows[0].getAttribute('aria-checked')!=='true'){rows[0].click();await sleep(150);menu=await open();rows=[...menu.querySelectorAll(%s)].filter(visible).filter(el=>norm(el.innerText||el.textContent)===requestedEffortLabel);}effortVerified=rows.length===1&&rows[0].getAttribute('aria-checked')==='true';}else{const containers=[...menu.querySelectorAll(%s)].filter(visible);if(containers.length!==1)throw new Error('effort slider unavailable or ambiguous');const slider=containers[0].querySelector('[role="slider"]');if(!slider)throw new Error('effort slider semantic input missing');const min=Number(slider.getAttribute('aria-valuemin')),max=Number(slider.getAttribute('aria-valuemax'));if(!Number.isSafeInteger(min)||!Number.isSafeInteger(max)||max-min+1<1||max-min+1>5)throw new Error('effort slider range invalid');const target=min+requestedEffortIndex;if(target>max)throw new Error('requested effort unavailable');const ticks=[...containers[0].querySelectorAll('[data-selected]')];if(ticks.length!==max-min+1)throw new Error('effort availability ticks unavailable');const tick=ticks[target-min];if(tick.getAttribute('data-locked')==='true')throw new Error('requested effort is locked');tick.click();await sleep(150);const current=Number(slider.getAttribute('aria-valuenow'));effortValue=current;effortVerified=current===target;}if(!effortVerified)throw new Error('requested effort could not be verified');}
if(requestedModel!==''){menu=await open();const rows=matchingModelRows();modelVerified=rows.length===1&&rows[0].getAttribute('aria-checked')==='true';if(!modelVerified)throw new Error('requested model selection changed after effort selection');}
const control=findControl();if(control&&(control.getAttribute('aria-expanded')==='true'||control.getAttribute('data-state')==='open'))control.click();}
return {available_models:available,model_verified:modelVerified,effort_verified:effortVerified,effort_value:effortValue};})()`,
		visiblePrelude(), jsString(model), effortIndex, jsString(effortLabel), jsString(ComposerSelector), jsString(EffortControlSelector), jsString(EffortMenuSelector), jsString(EffortItemSelector), jsString(EffortSliderContainerSelector), jsString(EffortItemSelector), jsString(EffortItemSelector), jsString(EffortItemSelector), jsString(EffortSliderContainerSelector))
}

func configureControls(ctx context.Context, tab browser.BrowserTab, model, effort string) (controlResult, error) {
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	index, label, err := normalizeEffort(effort)
	if err != nil {
		return controlResult{}, err
	}
	if model == "" && index < 0 {
		return controlResult{ModelVerified: true, EffortVerified: true}, nil
	}
	var result controlResult
	if err := tab.Evaluate(ctx, configureControlsExpression(model, index, label), &result); err != nil {
		return controlResult{}, err
	}
	return result, nil
}

func attachPromptExpression(prompt, connector, workspaceID string) string {
	route := connectorRoutePrefix(connector, workspaceID)
	return fmt.Sprintf(`/*codemcp:attach*/(()=>{%s
const norm=s=>(s||'').replace(/\r\n/g,'\n').trim();const canon=s=>(s||'').replace(/\s+/g,' ').trim().replace(/^@/,'').toLocaleLowerCase();const connector=%s;const workspace=%s;const mention=connector===''?'':'@'+connector;const target=canon(connector);const route=%s;const workspaceRoute=workspace+', ';const composers=[...document.querySelectorAll(%s)].filter(visible);if(composers.length!==1)throw new Error('expected one visible composer');const c=composers[0];
c.focus();document.execCommand('selectAll',false);document.execCommand('delete',false);const value=route+%s;if(!document.execCommand('insertText',false,value)){c.append(document.createTextNode(value));c.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:value}));}
const clone=c.cloneNode(true);const chosen=[...clone.querySelectorAll(%s)].filter(el=>[el.getAttribute('app-mention-display-name'),el.getAttribute('data-keyword'),el.getAttribute('aria-label')].some(v=>canon(v)===target));if(chosen.length>1)throw new Error('duplicate connector mentions');let count=0;if(connector!==''){if(chosen.length===1){chosen[0].remove();const raw=(clone.textContent||'').trimStart();if(raw.startsWith(workspaceRoute)){clone.textContent=raw.slice(workspaceRoute.length);count=1;}}else{const raw=clone.textContent||'';if(raw.startsWith(route)){clone.textContent=raw.slice(route.length);count=1;}}}return {text:norm(clone.textContent||''),connector_count:count};})()`,
		visiblePrelude(), jsString(connector), jsString(workspaceID), jsString(route), jsString(ComposerSelector), jsString(prompt), jsString(ConnectorMentionSelector))
}

func connectorRoutePrefix(connector, workspaceID string) string {
	connector = strings.TrimSpace(connector)
	workspaceID = strings.TrimSpace(workspaceID)
	if connector == "" {
		return ""
	}
	return "@" + connector + " " + workspaceID + ", "
}

func attachPrompt(ctx context.Context, tab browser.BrowserTab, prompt, connector, workspaceID string) (promptAttachResult, error) {
	var result promptAttachResult
	if err := tab.Evaluate(ctx, attachPromptExpression(prompt, connector, workspaceID), &result); err != nil {
		return promptAttachResult{}, err
	}
	return result, nil
}

func activateSendExpression() string {
	return fmt.Sprintf(`/*codemcp:send*/(()=>{%sconst composers=[...document.querySelectorAll(%s)].filter(visible);if(composers.length!==1)throw new Error('expected one visible composer');const form=composers[0].closest('form');if(!form)throw new Error('composer form missing');const sends=[...form.querySelectorAll(%s)].filter(visible);if(sends.length!==1)throw new Error('send control unavailable or ambiguous');const send=sends[0];if(send.disabled||send.getAttribute('aria-disabled')==='true')throw new Error('send control is disabled');send.click();return {activated:true};})()`, visiblePrelude(), jsString(ComposerSelector), jsString(SendButtonSelector))
}

func activateSend(ctx context.Context, tab browser.BrowserTab) (sendResult, error) {
	var result sendResult
	if err := tab.Evaluate(ctx, activateSendExpression(), &result); err != nil {
		return sendResult{}, err
	}
	return result, nil
}

func stopExpression() string {
	return fmt.Sprintf(`/*codemcp:stop*/(()=>{%sconst stops=[...document.querySelectorAll(%s)].filter(visible);if(stops.length>1)throw new Error('stop control is ambiguous');if(stops.length===1){stops[0].click();return {stopped:true};}return {stopped:false};})()`, visiblePrelude(), jsString(StopButtonSelector))
}

func stopTurn(ctx context.Context, tab browser.BrowserTab) (stopResult, error) {
	var result stopResult
	if err := tab.Evaluate(ctx, stopExpression(), &result); err != nil {
		return stopResult{}, err
	}
	return result, nil
}

func dismissTemporaryChatOnboarding(ctx context.Context, tab browser.BrowserTab) error {
	expression := fmt.Sprintf(`/*codemcp:onboarding*/(()=>{%s
const dialogs=[...document.querySelectorAll('[role="dialog"]')].filter(visible).filter(el=>{const t=(el.innerText||el.textContent||'').replace(/\s+/g,' ');return /Not in history/i.test(t)&&/No model training/i.test(t)&&/Memory off/i.test(t);});
if(dialogs.length>1)throw new Error('Temporary Chat onboarding is ambiguous');
if(dialogs.length===0)return false;
const buttons=[...dialogs[0].querySelectorAll('button')].filter(visible).filter(el=>(el.innerText||el.textContent||'').trim()==='Continue');
if(buttons.length!==1)throw new Error('Temporary Chat onboarding has no exact Continue action');
buttons[0].click();return true;})()`, visiblePrelude())
	var dismissed bool
	return tab.Evaluate(ctx, expression, &dismissed)
}

func normalizePrompt(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
}
