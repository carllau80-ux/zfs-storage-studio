<template>
  <el-config-provider :locale="epLocale">
    <router-view />
  </el-config-provider>
</template>
<script setup>
import { computed } from 'vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import { lang } from './i18n.js'
const epLocale = computed(() => (lang() === 'en' ? en : zhCn))
</script>
