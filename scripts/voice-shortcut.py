# Builds the "AI 비서 음성 호출" shortcut (unsigned); sign with: shortcuts sign --mode anyone -i <in> -o <out>.
# The token is asked on import (WFWorkflowImportQuestions), so the file holds no secret. Run with /usr/bin/python3.
import plistlib, uuid
OBJ='￼'
def U(): return str(uuid.uuid4()).upper()
def var(name): return {"Type":"Variable","VariableName":name}
def out(uid,name): return {"Type":"ActionOutput","OutputUUID":uid,"OutputName":name}
def tok(parts):
    s=""; att={}
    for p in parts:
        if isinstance(p,str): s+=p
        else:
            att["{%d, 1}"%len(s)]=p; s+=OBJ
    return {"Value":{"string":s,"attachmentsByRange":att},"WFSerializationType":"WFTextTokenString"}
def attach(a): return {"Value":a,"WFSerializationType":"WFTextTokenAttachment"}
A=[]
def act(ident,params): A.append({"WFWorkflowActionIdentifier":ident,"WFWorkflowActionParameters":params})

# 0-3: settings as variables
t1=U(); act("is.workflow.actions.gettext",{"UUID":t1,"WFTextActionText":"https://jarvis-be.choigonyok.com/agent/voice"})
act("is.workflow.actions.setvariable",{"WFVariableName":"API주소","WFInput":attach(out(t1,"텍스트"))})
t2=U(); act("is.workflow.actions.gettext",{"UUID":t2,"WFTextActionText":"여기에 음성 토큰"})
act("is.workflow.actions.setvariable",{"WFVariableName":"인증토큰","WFInput":attach(out(t2,"텍스트"))})
# 4: dictate
d=U(); act("is.workflow.actions.dictatetext",{"UUID":d,"WFSpeechLanguage":"ko-KR","WFDictateTextStopListening":"After Pause"})
# empty check
g1=U()
act("is.workflow.actions.conditional",{"GroupingIdentifier":g1,"WFControlFlowMode":0,"WFCondition":101,"WFInput":{"Type":"Variable","Variable":attach(out(d,"받아쓴 텍스트"))}})
act("is.workflow.actions.alert",{"WFAlertActionTitle":"AI 비서","WFAlertActionMessage":"음성 입력이 비어 있어요. 다시 말씀해 주세요.","WFAlertActionCancelButtonShown":False})
act("is.workflow.actions.exit",{})
act("is.workflow.actions.conditional",{"GroupingIdentifier":g1,"WFControlFlowMode":2})
# request
r=U()
act("is.workflow.actions.downloadurl",{"UUID":r,"WFURL":tok([var("API주소")]),"WFHTTPMethod":"POST",
  "ShowHeaders":True,
  "WFHTTPHeaders":{"Value":{"WFDictionaryFieldValueItems":[
     {"WFItemType":0,"WFKey":tok(["Authorization"]),"WFValue":tok(["Bearer ",var("인증토큰")])},
     {"WFItemType":0,"WFKey":tok(["Content-Type"]),"WFValue":tok(["application/json"])}]},"WFSerializationType":"WFDictionaryFieldValue"},
  "WFHTTPBodyType":"JSON",
  "WFJSONValues":{"Value":{"WFDictionaryFieldValueItems":[
     {"WFItemType":0,"WFKey":tok(["text"]),"WFValue":tok([out(d,"받아쓴 텍스트")])}]},"WFSerializationType":"WFDictionaryFieldValue"}})
a=U(); act("is.workflow.actions.getvalueforkey",{"UUID":a,"WFDictionaryKey":"answer","WFInput":attach(out(r,"URL의 콘텐츠"))})
g2=U()
act("is.workflow.actions.conditional",{"GroupingIdentifier":g2,"WFControlFlowMode":0,"WFCondition":101,"WFInput":{"Type":"Variable","Variable":attach(out(a,"사전 값"))}})
e=U(); act("is.workflow.actions.getvalueforkey",{"UUID":e,"WFDictionaryKey":"error","WFInput":attach(out(r,"URL의 콘텐츠"))})
act("is.workflow.actions.alert",{"WFAlertActionTitle":"AI 비서 오류","WFAlertActionMessage":tok(["요청이 실패했어요. ",out(e,"사전 값")]),"WFAlertActionCancelButtonShown":False})
act("is.workflow.actions.exit",{})
act("is.workflow.actions.conditional",{"GroupingIdentifier":g2,"WFControlFlowMode":2})
act("is.workflow.actions.speaktext",{"WFText":tok([out(a,"사전 값")]),"WFSpeakTextLanguage":"ko-KR","WFSpeakTextWait":True})
act("is.workflow.actions.showresult",{"Text":tok([out(a,"사전 값")])})

wf={"WFWorkflowActions":A,"WFWorkflowClientVersion":"2607.0.2","WFWorkflowMinimumClientVersion":900,"WFWorkflowMinimumClientVersionString":"900",
 "WFWorkflowName":"AI 비서 음성 호출","WFWorkflowIcon":{"WFWorkflowIconStartColor":463140863,"WFWorkflowIconGlyphNumber":59511},
 "WFWorkflowImportQuestions":[{"ActionIndex":2,"Category":"Parameter","DefaultValue":"","ParameterKey":"WFTextActionText","Text":"jarvis 음성 토큰을 붙여 넣으세요 (jarvis 웹 → 상태 → 폰 연결에서 복사)"}],
 "WFWorkflowInputContentItemClasses":[],"WFWorkflowOutputContentItemClasses":[],"WFWorkflowTypes":[],"WFWorkflowHasShortcutInputVariables":False,"WFQuickActionSurfaces":[]}
plistlib.dump(wf,open("AI 비서 음성 호출.unsigned.shortcut","wb"),fmt=plistlib.FMT_BINARY)
print(len(A),"actions")
