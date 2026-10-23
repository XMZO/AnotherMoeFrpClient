import type { MessageApi } from 'naive-ui'
import type { SaveResult } from '../api'
import { describeError } from '../errors'

/** 保存成功后的提示：配置在运行时说明是热更新还是重启了隧道（PLAN 6.3.5）。 */
export function saveFeedback(message: MessageApi, res: SaveResult) {
  if (res.applyError) {
    message.warning(`已保存，但没能应用到运行中的隧道：${describeError(res.applyError)}`, { duration: 6000 })
  } else if (res.applied === 'hot') {
    message.success('已保存，并已热更新到运行中的隧道（连接不断）')
  } else if (res.applied === 'restart') {
    message.info('已保存，节点连接参数有变化，正在重启隧道')
  } else {
    message.success('已保存')
  }
}
