import { afterEach, expect, test } from "bun:test";
import { preferredDramaModel } from "../src/lib/drama-model-preferences";
import { defaultModelCapabilityConfig } from "../src/lib/model-capabilities";
import { systemChannelModelChannels } from "../src/lib/user-session";
import { defaultConfig, normalizeConfigSnapshot } from "../src/stores/use-config-store";
import { normalizeDramaRecentModels, rememberAcceptedDramaTask, useDramaModelPreferencesStore } from "../src/stores/use-drama-model-preferences-store";
import type { PublicChannelModel } from "../src/services/api/logical-models";
import { storyboardReferenceSource } from "../src/pages/projects/workbench/detail/storyboard-reference";

function fixture() {
    const model = (modelKey: string, capability: "text" | "image" | "video"): PublicChannelModel => {
        const protocol = capability === "text" ? "chat-completion" : capability === "image" ? "openai-image" : "seedance";
        return { id:modelKey, modelKey, displayName:modelKey, channelLabel:"", description:"", icon:"", capability, protocol, available:true, pricingMode:"provider", priceLabel:"", capabilityConfig:defaultModelCapabilityConfig(protocol,modelKey), priceTiers:[] };
    };
    return normalizeConfigSnapshot({ config: { ...defaultConfig, channels:systemChannelModelChannels([{id:"a",name:"test",displayName:"test",models:[model("gpt-6.1-sol","text"),model("other-text","text"),model("image-a","image"),model("image-b","image"),model("seedance-2.5","video")]}]), textModel:"a::other-text", imageModel:"a::image-a", videoModel:"a::seedance-2.5" } }).config;
}
afterEach(() => useDramaModelPreferencesStore.setState({models:{}}));

test("首次文本默认真实可用 gpt，最近使用优先且不改全局默认", () => {
    const config=fixture();
    expect(preferredDramaModel(config,"text",{})).toBe("a::gpt-6.1-sol");
    expect(preferredDramaModel(config,"text",{text:"a::other-text"})).toBe("a::other-text");
    expect(config.textModel).toBe("a::other-text");
});
test("失效和不适配的最近模型按能力回退，不使用空或错误类型", () => {
    const config=fixture();
    expect(preferredDramaModel(config,"text",{text:"gone::model"})).toBe("a::gpt-6.1-sol");
    expect(preferredDramaModel(config,"image",{image:"a::other-text"},"a::image-b")).toBe("a::image-b");
    expect(preferredDramaModel(config,"image",{image:"a::image-b"},"a::image-a",m=>m!=="a::image-b")).toBe("a::image-a");
    expect(preferredDramaModel({...config,channels:[],textModels:[]},"text",{})).toBe("");
});
test("接受的短剧任务分别记住完整渠道标识，画布和其他账号不污染记录", () => {
    rememberAcceptedDramaTask({type:"canvas_text",model:"a::other-text",clientContext:{domainProjectId:"p"}});
    rememberAcceptedDramaTask({type:"canvas_image",model:"image-b",inputJson:JSON.stringify({config:{channelId:"a",channelModelKey:"image-b"},metadata:{domainProjectId:"p"}})});
    expect(useDramaModelPreferencesStore.getState().models).toEqual({text:"a::other-text",image:"a::image-b"});
    rememberAcceptedDramaTask({type:"canvas_text",model:"canvas::model"});
    rememberAcceptedDramaTask({userId:"different-user",type:"canvas_video",model:"foreign::video",clientContext:{domainProjectId:"p"}});
    expect(useDramaModelPreferencesStore.getState().models).toEqual({text:"a::other-text",image:"a::image-b"});
});
test("偏好只保留三类模型，不保留密钥和媒体", () => {
    expect(normalizeDramaRecentModels({text:" a::gpt-6.1-sol ",image:12,video:"",apiKey:"secret",images:["data:..."]})).toEqual({text:"a::gpt-6.1-sol"});
});
test("白模附件来源可识别，普通分镜和损坏元数据不误标", () => {
    expect(storyboardReferenceSource({metadataJson:JSON.stringify({role:"continuity_reference",continuityReference:{sourceShotTitle:"A",sourceVideoResourceId:"video-a",frameTimeSeconds:29.967}})})).toEqual({title:"A",videoResourceId:"video-a",timeSeconds:29.967});
    expect(storyboardReferenceSource({metadataJson:"{}"})).toBeUndefined();
    expect(storyboardReferenceSource({metadataJson:"{"})).toBeUndefined();
});
