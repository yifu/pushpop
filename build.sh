dir=$(git rev-parse --show-toplevel)

cd $dir/push
CGO_ENABLED=0 go build ./

cd $dir/pop
CGO_ENABLED=0 go build ./
