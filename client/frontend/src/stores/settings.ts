import { defineStore } from 'pinia'
import { ref } from 'vue'
import { SettingsService, type Settings } from '../api'
import { toAppError } from '../errors'

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<Settings | null>(null)

  async function load() {
    settings.value = await SettingsService.Get()
  }

  async function update(patch: Partial<Settings>) {
    if (!settings.value) await load()
    try {
      settings.value = await SettingsService.Update({ ...settings.value!, ...patch })
    } catch (e) {
      throw toAppError(e)
    }
  }

  return { settings, load, update }
})
