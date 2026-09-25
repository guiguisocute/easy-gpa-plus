import type { ExportKind } from '@/api/types'

export const EXPORT_PRODUCTS: { key: ExportKind; name: string; fmt: string; desc: string }[] = [
  { key: 'summary', name: '汇总表', fmt: 'xlsx', desc: '一人一行，按学号排。四项得分和排名、总分、班级排名、三好、奖学金档位都在里面' },
  { key: 'detail', name: '逐人明细', fmt: 'xlsx', desc: '一人一张表，写清他的得分、材料、审核意见和申诉记录' },
  { key: 'archive', name: '佐证归档包', fmt: 'zip', desc: '佐证按学号分文件夹。同一个学生重复用到的同一份文件只存一次' },
  {
    key: 'college',
    name: '参考报表',
    fmt: 'zip',
    desc: '按参考模板生成 4 个独立文件：2 份 Word、2 份 Excel。下载后解压，补齐待填内容即可；分数已折算，保留两位小数',
  },
]
