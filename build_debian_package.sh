dir=$(git rev-parse --show-toplevel)
cd $dir

pkg_rev=3
pkg_dir=pushpop_0.0-$pkg_rev

echo $pkg_dir
mkdir -p $pkg_dir/usr/local/bin
cp -r DEBIAN $pkg_dir/
sed -i "s/Version: 0.0-2/Version: 0.0-$pkg_rev/g" $pkg_dir/DEBIAN/control
cp -v push/push $pkg_dir/usr/local/bin/
cp -v pop/pop $pkg_dir/usr/local/bin/
dpkg-deb --build $pkg_dir
rm -r $pkg_dir
