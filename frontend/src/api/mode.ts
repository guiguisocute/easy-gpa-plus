/* 运行开关。

   这里曾经还有一个 IS_MOCK：后端只有骨架时，27 个页面靠 src/data/demo.ts 独立跑。
   后端接通后它被删掉了——继续留着意味着每个页面维护两套数据形状，
   而演示数据永远追不上真实接口的字段变化，最后只会骗自己。 */

/** 尚未接入运行时状态的旧 AI 占位区仍使用这个构建期开关。
    学生材料整理入口已经改为读取 /ai/status，由运维 Agent 页面即时控制。 */
export const AI_ENABLED = import.meta.env.VITE_AI_ENABLED === 'true'
