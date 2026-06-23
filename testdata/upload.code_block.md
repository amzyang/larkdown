Go 示例：

```go
func main() {
	client := core.NewClient(appID, appSecret)
	doc, err := client.GetDocxDocument(docToken)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(doc.Title)
}
```

Python 示例：

```python
import requests

def download_doc(token: str) -> dict:
    url = f"https://open.feishu.cn/open-apis/docx/v1/documents/{token}"
    resp = requests.get(url, headers={"Authorization": f"Bearer {token}"})
    return resp.json()
```

JSON 配置示例：

```json
{
  "appId": "cli_xxxx",
  "appSecret": "xxxx",
  "output": "./downloads"
}
```
<!--
source: https://feishu.cn/wiki/H4lKw1uLLise7DkzNWdcmlKSnrs
-->
