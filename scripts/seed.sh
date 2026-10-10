#!/bin/sh
# Seed sample bookmarks. Usage: scripts/seed.sh [base_url]   (default http://localhost:8080)
BASE=${1:-http://localhost:8080}
while IFS='|' read -r title url; do
  [ -z "$url" ] && continue
  curl -fsS -X POST "$BASE/api/bookmarks" -H 'Content-Type: application/json' \
    -d "{\"title\":\"$title\",\"url\":\"$url\"}" >/dev/null && echo "added $title" || echo "FAILED $title"
done <<'LIST'
GitHub|https://github.com
Go|https://go.dev
Google|https://www.google.com
哔哩哔哩|https://www.bilibili.com
知乎|https://www.zhihu.com
百度|https://www.baidu.com
V2EX|https://www.v2ex.com
稀土掘金|https://juejin.cn
Stack Overflow|https://stackoverflow.com
YouTube|https://www.youtube.com
Wikipedia|https://www.wikipedia.org
Reddit|https://www.reddit.com
Hacker News|https://news.ycombinator.com
MDN|https://developer.mozilla.org
Docker Hub|https://hub.docker.com
Gitee|https://gitee.com
淘宝|https://www.taobao.com
京东|https://www.jd.com
豆瓣|https://www.douban.com
微博|https://weibo.com
CSDN|https://www.csdn.net
X|https://x.com
Hugging Face|https://huggingface.co
npm|https://www.npmjs.com
Cloudflare|https://www.cloudflare.com
Notion|https://www.notion.so
LIST
