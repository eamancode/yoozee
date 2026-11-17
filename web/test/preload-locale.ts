import "@/i18n";
import { useLocaleStore } from "@/stores/use-locale-store";

// 组件回归直接挂载，并不经过 application.tsx。初始化相同的词典并固定中文断言的语言。
useLocaleStore.setState({ locale: "zh-CN" });
