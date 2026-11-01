import { defineMessages } from '../core';

/** Traffic chart, its panel and the top lists of the admin overview. */
export const charts = defineMessages({
  en: {
    summary: 'Traffic from {from} to {to}: {calls} calls, {denied} denied.',
    peak: 'Peak of {n} calls at {time}.',
    legendCalls: 'calls',
    legendDenied: 'denied',
    emptyPeriod: 'No calls in this period.',
    loadingTraffic: 'Loading traffic',
    topToolsTitle: 'Busiest tools',
    topDeniesTitle: 'Most frequent denials',
    noDenies: 'No denials in this period.',
    deniedCount: '{n} denied',
    p95: 'p95 {value}',
  },
  'pt-BR': {
    summary: 'Tráfego de {from} a {to}: {calls} chamadas, {denied} negadas.',
    peak: 'Pico de {n} chamadas às {time}.',
    legendCalls: 'chamadas',
    legendDenied: 'negadas',
    emptyPeriod: 'Sem chamadas neste período.',
    loadingTraffic: 'Carregando o tráfego',
    topToolsTitle: 'Tools mais usadas',
    topDeniesTitle: 'Negações mais frequentes',
    noDenies: 'Nenhuma negação neste período.',
    deniedCount: '{n} negadas',
    p95: 'p95 {value}',
  },
});
