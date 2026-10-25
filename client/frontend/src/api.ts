// 对 Wails 生成的绑定统一导出。调用失败时抛出的异常用 errors.ts 的 toAppError 规整。
export { Events } from '@wailsio/runtime'
export { AppService, AuthService, ImageService, ProfileService, SettingsService, ShareService, TunnelService, VnetService } from '../bindings/moefrp/client/internal/services'

export type { ProfileSummary, SaveResult, StartupInfo, VersionInfo, LogoutInfo, SubscriptionSaveResult, UpdateInfo, License, ImageInfo, OfficialNodeView } from '../bindings/moefrp/client/internal/services/models'
export type { Profile, Node, Proxy, Visitor, SharePayload, P2PTarget, P2PBind, SubscriptionParams } from '../bindings/moefrp/shared/model/models'
export type { Status, ProxyStatus, LogLine, PingResult } from '../bindings/moefrp/client/internal/engine/models'
export type { Settings } from '../bindings/moefrp/client/internal/store/models'
export type {
  AccountState,
  Status as SyncStatus,
  Changed,
  Conflict,
  DiffItem,
  PushError,
  RemoteUpdate,
  ResolveResult,
} from '../bindings/moefrp/client/internal/cloudsync/models'
export type {
  UserInfo,
  Share,
  SharePreview,
  SubscriptionSummary,
  CreateShareRequest,
  UpdateShareRequest,
  PreviewNode,
  PreviewTarget,
  PreviewProxy,
} from '../bindings/moefrp/shared/api/models'
