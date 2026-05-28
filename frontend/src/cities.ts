export interface CityOption {
  name: string   // displayed to user + matches DB location.cityName
  slug: string   // OLX URL segment (Russian-style transliteration)
  region?: string
}

export interface CategoryOption {
  name: string
  slug: string
}

export const CITIES: CityOption[] = [
  { name: "Київ",      slug: "kiev",     region: "Київська область" },
  { name: "Буча",      slug: "bucha",    region: "Київська область" },
  { name: "Ірпінь",   slug: "irpen",    region: "Київська область" },
  { name: "Гостомель", slug: "gostomel", region: "Київська область" },
]

export const CATEGORIES: CategoryOption[] = [
  { name: "Продаж квартир",               slug: "prodazha-kvartir"    },
  { name: "Продаж будинків",              slug: "prodazha-domov"       },
  { name: "Довгострокова оренда квартир", slug: "arenda-kvartir"       },
  { name: "Земельні ділянки",             slug: "zemlya"               },
  { name: "Комерційна нерухомість",       slug: "prodazha-pomescheniy" },
]