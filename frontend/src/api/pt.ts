import { client, request } from './client'
import type {
  PTCalendar,
  PTContract,
  PTContractRequest,
  PTLedger,
  PTMatrix,
  PTOverview,
  PTPublishPreview,
  PTPublishRequest,
  PTSiteProfile,
  PTSitesResponse,
  PTVaultDetail,
  PTVaultList,
  PTVaultReviveResult,
} from '@/types/pt'

// PT 中心接口。除资料包下载（zip 二进制）外都走统一 ApiResponse 解包。
export const ptApi = {
  overview: () => request<PTOverview>(client.get('/pt/overview')),
  ledger: () => request<PTLedger>(client.get('/pt/ledger')),
  calendar: (month?: string) => request<PTCalendar>(client.get('/pt/calendar', { params: month ? { month } : {} })),
  vault: (q?: string) => request<PTVaultList>(client.get('/pt/vault', { params: q ? { q } : {} })),
  vaultDetail: (id: string) => request<PTVaultDetail>(client.get(`/pt/vault/${encodeURIComponent(id)}`)),
  vaultRevive: (id: string) => request<PTVaultReviveResult>(client.post(`/pt/vault/${encodeURIComponent(id)}/revive`)),
  vaultRemove: (id: string) => request<{ deleted: boolean }>(client.delete(`/pt/vault/${encodeURIComponent(id)}`)),
  // 留档种子的二进制下载：必须走 axios 才能带上鉴权头，不能用 <a href>
  vaultTorrent: async (id: string) => {
    const resp = await client.get(`/pt/vault/${encodeURIComponent(id)}/file`, { responseType: 'blob' })
    return resp.data as Blob
  },
  matrix: () => request<PTMatrix>(client.get('/pt/matrix')),
  publishPreview: (body: PTPublishRequest) => request<PTPublishPreview>(client.post('/pt/publish/preview', body)),
  // 资料包是 zip 流，直接拿到 Blob 由调用方触发下载
  publishKit: async (body: PTPublishRequest, torrent?: File | null) => {
    const form = new FormData()
    form.append('payload', JSON.stringify(body))
    if (torrent) form.append('torrent', torrent)
    const resp = await client.post('/pt/publish/kit', form, { responseType: 'blob' })
    return resp.data as Blob
  },
  sites: () => request<PTSitesResponse>(client.get('/pt/sites')),
  saveSite: (profile: PTSiteProfile) => request<PTSiteProfile>(client.post('/pt/sites', profile)),
  deleteSite: (id: string) => request<{ deleted: boolean }>(client.delete(`/pt/sites/${encodeURIComponent(id)}`)),
  seedSites: () => request<{ created: number }>(client.post('/pt/sites/seed')),
  sample: () => request<{ sampled: boolean; count: number }>(client.post('/pt/sample')),
  // 保种契约：只写「承诺」，不触发暂停/删除
  setContract: (body: PTContractRequest) => request<PTContract>(client.post('/pt/contract', body)),
  deleteContract: (hash: string) => request<{ deleted: boolean }>(client.delete(`/pt/contract/${encodeURIComponent(hash)}`)),
}